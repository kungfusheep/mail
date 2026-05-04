package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/contacts"
	imapprov "github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/provider"
	smtpprov "github.com/kungfusheep/mail/smtp"
	"github.com/kungfusheep/riffkey"
)

// newComposeID returns a stable identifier for a freshly-opened compose
// session. Random hex rather than a UID or timestamp so the drafts
// projection can never confuse it with something that came from IMAP.
func newComposeID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "draft-" + hex.EncodeToString(b), nil
}

type AppTheme struct {
	BG      Color
	Bright  Color // selected item, emphasis
	FG      Color // active pane content
	Subtle  Color // dates, senders, secondary info
	Dim     Color // inactive pane content
	Muted   Color // help bar, status
	Accent  Color
	SelBG   Color
	GroupBG Color
}

type mailCommand struct {
	Label       string
	Description string
	Key         string
	Section     string
	Action      func()
	Selected    bool
}

var themeDark = AppTheme{
	BG:      Hex(0x1a1a1a),
	Bright:  Hex(0xeeeeee),
	FG:      Hex(0xb0b0b0),
	Subtle:  Hex(0x777777),
	Dim:     Hex(0x565656),
	Muted:   Hex(0x3a3a3a),
	Accent:  Hex(0xe60012),
	SelBG:   Hex(0x2e2e2e),
	GroupBG: Hex(0x242424),
}

var themeLight = AppTheme{
	BG:      Hex(0xf6f6f6),
	Bright:  Hex(0x111111),
	FG:      Hex(0x333333),
	Subtle:  Hex(0x777777),
	Dim:     Hex(0xaaaaaa),
	Muted:   Hex(0xcccccc),
	Accent:  Hex(0xe60012),
	SelBG:   Hex(0xe8e8e8),
	GroupBG: Hex(0xeeeeee),
}

func fuzzyMatch(str, pattern string) bool {
	if pattern == "" {
		return true
	}
	i := 0
	for _, r := range str {
		if i < len(pattern) && r == rune(pattern[i]) {
			i++
		}
	}
	return i == len(pattern)
}

func main() {
	logFile, err := os.OpenFile(
		filepath.Join(os.TempDir(), "mail.log"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644,
	)
	if err == nil {
		log.SetOutput(logFile)
		// redirect stderr to the log so panics/runtime errors don't corrupt the TUI
		syscall.Dup2(int(logFile.Fd()), int(os.Stderr.Fd()))
		defer logFile.Close()
	}
	log.Println("starting mail")

	app := NewApp()
	app.SetDefaultStyle(Style{FG: themeDark.FG, BG: themeDark.BG})

	db, err := cache.New()
	if err != nil {
		log.Fatal(err)
	}
	// sweep any leftover empty-body drafts from earlier writes
	_ = db.GCEmptyDrafts()

	cfg, err := imapprov.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}

	mb := mailbox.New(db, cfg.Email)
	smtp := smtpprov.New(smtpprov.Config{
		Server:   cfg.SMTPServer,
		Email:    cfg.Email,
		Password: cfg.Password,
	})

	// load from cache
	mb.LoadFolders()
	mb.BuildFolderDisplay(false)
	// Tell the cache which IMAP folder id represents Drafts — draft-table
	// writes publish on that label so the mailbox subscriber refreshes the
	// UI. Without this wiring the drafts view silently goes stale.
	if draftsID := mb.FolderIDByDisplayName("Drafts"); draftsID != "" {
		db.SetDraftsLabel(draftsID)
	}
	mb.LoadThreads()
	mb.BuildThreadDisplay()
	mb.SetSelected(0)
	mb.LoadConversation(0, nil)

	t := themeDark

	// inbox view state
	var (
		folderSel   int
		threadSel   int
		labelsOpen  bool
		helpOpen    bool
		helpRef     NodeRef
		frame       int
		statusText  = "Inbox"
		searchQuery string

		// pane styles — active uses FG, inactive uses dim
		folderStyle     = Style{FG: t.Dim}
		threadStyle     = Style{FG: t.FG}
		previewStyle    = Style{FG: t.Dim}
		folderListStyle = Style{FG: t.Dim}
		folderSelStyle  = Style{FG: t.Dim}
		threadListStyle = Style{FG: t.FG}
		pane            = 1

		undoStack []func()
	)

	updateFocus := func() {
		folderStyle = Style{FG: t.Dim}
		threadStyle = Style{FG: t.Dim}
		folderListStyle = Style{FG: t.Dim}
		folderSelStyle = Style{FG: t.Dim}
		threadListStyle = Style{FG: t.Dim}
		previewStyle = Style{FG: t.Dim}
		switch pane {
		case 0:
			folderStyle = Style{FG: t.FG}
			folderListStyle = Style{FG: t.FG}
			folderSelStyle = Style{FG: t.Bright}
		case 1:
			threadStyle = Style{FG: t.FG}
			threadListStyle = Style{FG: t.FG}
		case 2:
			previewStyle = Style{FG: t.FG}
		}
	}

	// shimmer catalogue — navigate with , and . (prev/next). Label shows
	// in the status bar. Only the active variant is included in the view
	// tree (via If), so inactive ones pay no per-frame cost.
	var (
		// wormholeSpeed float64 = 6.0
		shimmerIdx   int = 0
		shimmerLabel     = ""

		// wormhole family toggles
		// onWormhole, onWStreaks, onWPulse, onWDepth, onWLayered, onWShear, onWLab, onSilEcho bool
	)
	// focused on wormhole family while we iterate; other effects are preserved
	// in effects.go but removed from the view template below.
	variantNames := []string{
		"wormhole (base)",
		"wormhole warp",
		"wormhole drag",
		"wormhole surge",
		"wormhole core",
		"wormhole turbulence (baseline)",
		"wormhole lab (iterating)",
		"silhouette echo",
	}

	// applyVariant := func() {
	// 	onWormhole, onWStreaks, onWPulse, onWDepth, onWLayered, onWShear, onWLab, onSilEcho = false, false, false, false, false, false, false, false
	// 	shimmerLabel = variantNames[shimmerIdx]
	// 	wormholeSpeed = 6.0
	// 	switch shimmerIdx {
	// 	case 0:
	// 		onWormhole = true
	// 	case 1:
	// 		onWStreaks = true
	// 	case 2:
	// 		onWPulse = true
	// 	case 3:
	// 		onWDepth = true
	// 	case 4:
	// 		onWLayered = true
	// 	case 5:
	// 		onWShear = true
	// 	case 6:
	// 		onWLab = true
	// 	case 7:
	// 		onSilEcho = true
	// 	}
	// }
	// applyVariant()

	// continuous frame requests for time-based animation
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			app.RequestRender()
		}
	}()

	// transition: mailbox → compose ("settle and rise")
	composeTransition := NewViewTransition(940*time.Millisecond, t.BG, Hex(0x3a3a3a))

	// compose view — editor theme matches mail palette so the compose view and
	// mailbox share the same dark background. Earlier we left Background unset
	// because it seemed to trigger a top-row-loss bug; that turned out to be
	// the emoji/wide-rune width issue (fixed in glyph).
	composeTheme := compose.Theme{
		Name:          "mail",
		Text:          t.FG,
		Background:    t.BG,
		Bold:          Style{Attr: AttrBold},
		Italic:        Style{Attr: AttrItalic},
		Underline:     Style{Attr: AttrUnderline},
		Strikethrough: Style{Attr: AttrStrikethrough},
		Code:          Style{FG: t.FG},
		Accent:        Style{FG: t.Accent},
		Heading1:      Style{FG: t.Bright, Attr: AttrBold},
		Heading2:      Style{FG: t.Bright, Attr: AttrBold},
		Heading3:      Style{FG: t.Bright, Attr: AttrBold},
		Heading4:      Style{FG: t.FG, Attr: AttrBold},
		Heading5:      Style{FG: t.FG, Attr: AttrBold},
		Heading6:      Style{FG: t.FG, Attr: AttrBold},
		Blockquote:    Style{FG: t.Subtle, Attr: AttrItalic},
		CodeBlock:     Style{FG: t.FG},
		ListBullet:    Style{FG: t.Subtle},
		Callout:       Style{FG: t.Accent},
		Divider:       Style{FG: t.Muted, Attr: AttrDim},
		Cursor: compose.CursorColors{
			Normal: t.Bright,
			Insert: Hex(0x5af78e),
			Visual: Hex(0xf4f99d),
		},
		DialogueCharacter:     Style{FG: t.Bright, Attr: AttrBold},
		DialogueText:          Style{FG: t.FG},
		DialogueParenthetical: Style{FG: t.Subtle, Attr: AttrItalic},
		FrontMatterKey:        Style{FG: t.Subtle, Attr: AttrBold},
		FrontMatterValue:      Style{FG: t.FG},
		Dimmed:                Style{FG: t.Dim, Attr: AttrDim},
	}

	editor := compose.NewEditor(compose.NewDocument(), "")
	editor.SetTheme(composeTheme)
	editor.SetApp(app)
	editor.StartSpellResultWorker(app.RequestRender)
	comp := setupComposeView(app, editor, mb, smtp, db, &statusText, &frame, composeTransition, t)

	var convView *ScrollViewC
	var loadPreview func()

	// imap connection (nil until Authenticate succeeds in the goroutine below)
	var imap *imapprov.IMAP

	// idle + cache subscription, per active label. watchLabel cancels any
	// prior watcher and starts a new pair:
	//   - a goroutine listening to cache.Subscribe(label); fires a UI refresh
	//     whenever the network (or anything else) writes to that label.
	//   - IDLE on a second IMAP connection; on change it triggers SyncThreads,
	//     which writes to cache, which in turn fires the subscriber above.
	var (
		idleCancel context.CancelFunc
		labelUnsub func()
	)

	watchLabel := func(label string) {
		if idleCancel != nil {
			idleCancel()
			idleCancel = nil
		}
		if labelUnsub != nil {
			labelUnsub()
			labelUnsub = nil
		}
		if label == "" || imap == nil {
			return
		}

		labelUnsub = mb.Watch(label, func() {
			mb.SetSelected(threadSel)
			mb.BuildFolderDisplay(labelsOpen)
			// refresh the preview too — the data under it may have changed
			// (e.g. reconcileDrafts just backfilled the selected draft's
			// body, or a sync pulled the rest of a conversation).
			if loadPreview != nil {
				loadPreview()
			}
			app.RequestRender()
		})

		ctx, cancel := context.WithCancel(context.Background())
		idleCancel = cancel
		go func() {
			if err := imap.Idle(ctx, label, func() {
				log.Printf("idle: change on %s, syncing", label)
				if err := mb.SyncThreads(); err != nil {
					log.Printf("idle sync: %v", err)
				}
			}); err != nil && ctx.Err() == nil {
				log.Printf("idle %s: %v", label, err)
			}
		}()
	}

	// connect and sync in background
	go func() {
		imap = imapprov.New(cfg)
		if err := imap.Authenticate(); err != nil {
			statusText = fmt.Sprintf("imap: %v", err)
			app.RequestRender()
			return
		}
		mb.SetIMAP(imap)
		log.Println("imap: authenticated")

		if err := mb.SyncFolders(); err != nil {
			statusText = fmt.Sprintf("sync: %v", err)
			app.RequestRender()
			return
		}
		mb.BuildFolderDisplay(labelsOpen)
		// Fresh folders may reveal a Drafts label the cached view didn't have —
		// re-set so publish routing is current.
		if draftsID := mb.FolderIDByDisplayName("Drafts"); draftsID != "" {
			db.SetDraftsLabel(draftsID)
		}
		app.RequestRender()

		mb.SyncSent()
		mb.ProcessPendingCommands()

		if err := mb.SyncThreads(); err != nil {
			statusText = fmt.Sprintf("sync: %v", err)
		}
		mb.BuildFolderDisplay(labelsOpen)
		mb.BuildThreadDisplay()
		mb.SetSelected(threadSel)
		loadPreview()
		app.RequestRender()

		go cacheContacts(db)

		// start live updates on the active label
		watchLabel(mb.ActiveFolderID())
	}()

	syncThreadsFromNetwork := func() {
		mb.ProcessPendingCommands()
		if err := mb.SyncThreads(); err != nil {
			statusText = fmt.Sprintf("sync: %v", err)
		}
		mb.BuildFolderDisplay(labelsOpen)
		mb.BuildThreadDisplay()
		mb.SetSelected(threadSel)
		app.RequestRender()
	}

	loadPreview = func() {
		mb.LoadConversation(threadSel, func() {
			if convView != nil {
				convView.Refresh()
			}
			app.RequestRender()
		})
		if convView != nil {
			convView.Refresh()
		}
		app.RequestRender()
	}

	handleEnter := func() {
		// Drafts folder: Enter resumes the draft in the composer rather than
		// opening a preview — the thread is a draft-in-progress, not a
		// conversation to read. Thread id in this folder is the stable
		// local draft id; ResumeDraft reads straight from the drafts table.
		if mb.ActiveFolderCanonical() == "Drafts" {
			if t := mb.SelectedThread(threadSel); t != nil {
				comp.ResumeDraft(t.ID)
			}
			return
		}
		if msg := mb.SelectedMessage(threadSel); msg != nil {
			mb.LoadPreview(*msg, app.Size().Width)
			mb.MarkRead(threadSel)
			pane = 2
			updateFocus()
			return
		}
		mb.ToggleThread(threadSel)
		mb.MarkRead(threadSel)
		loadPreview()
	}

	pushUndo := func(undo func(), desc string) {
		if undo != nil {
			undoStack = append(undoStack, undo)
			statusText = desc + " — u to undo"
		}
	}

	clampThreadSel := func() {
		if threadSel >= mb.ThreadLen() {
			threadSel = mb.ThreadLen() - 1
		}
		if threadSel < 0 {
			threadSel = 0
		}
		mb.SetSelected(threadSel)
	}

	loadFolder := func() {
		if folderSel == mb.CanonEnd() {
			return
		}
		actualIdx := folderSel
		if folderSel > mb.CanonEnd() {
			actualIdx = folderSel - 2
		}
		if actualIdx >= mb.FolderCount() {
			return
		}
		mb.SelectFolder(actualIdx)
		mb.LoadThreads()
		mb.BuildThreadDisplay()
		threadSel = 0
		mb.SetSelected(0)
		loadPreview()
		undoStack = nil
		statusText = mb.FolderName(folderSel)
		go syncThreadsFromNetwork()
		watchLabel(mb.ActiveFolderID())
	}

	startSearch := func() {
		searchQuery = ""
		app.HideCursor()
		app.PushView("search")
	}

	var (
		omniboxQuery    string
		omniboxSel      int
		omniboxOpen     bool
		omniboxEmpty    bool
		omniboxHeight   int16 = 20
		omniboxMaxRows        = 6
		omniboxItems    []mailCommand
		omniboxFiltered []mailCommand
		omniboxVisible  []mailCommand
	)

	updateOmniboxLayout := func(width, height int) {
		h := height * 6 / 10
		if h < 10 {
			h = 10
		}
		if h > height-4 {
			h = height - 4
		}
		if h < 6 {
			h = 6
		}
		omniboxHeight = int16(h)

		rows := (h - 6) / 3
		if rows < 1 {
			rows = 1
		}
		omniboxMaxRows = rows
		_ = width
	}

	size := app.Size()
	updateOmniboxLayout(size.Width, size.Height)

	refreshOmniboxVisible := func() {
		omniboxVisible = omniboxVisible[:0]
		omniboxEmpty = len(omniboxFiltered) == 0
		if len(omniboxFiltered) == 0 {
			return
		}
		start := 0
		if omniboxSel >= omniboxMaxRows {
			start = omniboxSel - omniboxMaxRows + 1
		}
		end := start + omniboxMaxRows
		if end > len(omniboxFiltered) {
			end = len(omniboxFiltered)
		}
		omniboxVisible = append(omniboxVisible, omniboxFiltered[start:end]...)
	}

	refreshOmniboxSelection := func() {
		if omniboxSel >= len(omniboxFiltered) {
			omniboxSel = len(omniboxFiltered) - 1
		}
		if omniboxSel < 0 {
			omniboxSel = 0
		}
		for i := range omniboxFiltered {
			omniboxFiltered[i].Selected = i == omniboxSel
		}
		refreshOmniboxVisible()
	}

	refreshOmnibox := func() {
		q := strings.ToLower(strings.TrimSpace(omniboxQuery))
		omniboxFiltered = omniboxFiltered[:0]
		for _, item := range omniboxItems {
			haystack := strings.ToLower(item.Label + " " + item.Description + " " + item.Key + " " + item.Section)
			if q == "" || strings.Contains(haystack, q) || fuzzyMatch(haystack, q) {
				omniboxFiltered = append(omniboxFiltered, item)
			}
		}
		refreshOmniboxSelection()
	}

	app.OnResize(func(width, height int) {
		updateOmniboxLayout(width, height)
		refreshOmniboxVisible()
	})

	var omniboxRouter *riffkey.Router

	openOmnibox := func() {
		if omniboxOpen {
			return
		}
		omniboxQuery = ""
		omniboxSel = 0
		omniboxOpen = true
		refreshOmnibox()
		app.HideCursor()
		app.Push(omniboxRouter)
		app.RequestRender()
	}

	closeOmnibox := func() {
		if !omniboxOpen {
			return
		}
		omniboxQuery = ""
		omniboxOpen = false
		refreshOmnibox()
		app.Pop()
		app.HideCursor()
		app.RequestRender()
	}

	moveOmnibox := func(delta int) {
		if len(omniboxFiltered) == 0 {
			omniboxSel = 0
			return
		}
		omniboxSel += delta
		if omniboxSel < 0 {
			omniboxSel = len(omniboxFiltered) - 1
		}
		if omniboxSel >= len(omniboxFiltered) {
			omniboxSel = 0
		}
		refreshOmniboxSelection()
	}

	pageOmnibox := func(delta int) {
		if len(omniboxFiltered) == 0 {
			omniboxSel = 0
			return
		}
		omniboxSel += delta
		if omniboxSel < 0 {
			omniboxSel = 0
		}
		if omniboxSel >= len(omniboxFiltered) {
			omniboxSel = len(omniboxFiltered) - 1
		}
		refreshOmniboxSelection()
	}

	threadAction := func(label string, fn func()) {
		if mb.ThreadLen() == 0 {
			statusText = label + ": no thread selected"
			return
		}
		fn()
	}

	omniboxItems = []mailCommand{
		{Label: "Compose New", Description: "start a fresh message", Key: "c", Section: "compose", Action: func() { comp.Open() }},
		{Label: "Resume Draft", Description: "continue the latest saved draft", Key: "C", Section: "compose", Action: func() { comp.ResumeLast() }},
		{Label: "Reply To Selected Thread", Description: "reply to the current conversation", Key: "r", Section: "compose", Action: func() {
			threadAction("reply", func() {
				if t := mb.SelectedThread(threadSel); t != nil {
					if row := mb.ThreadRowAt(threadSel); row != nil && row.MsgIdx < 0 && mb.ActiveFolderCanonical() == "Drafts" {
						comp.ResumeDraft(t.ID)
						return
					}
					comp.Open()
					comp.SetupReply(*t)
				}
			})
		}},
		{Label: "Refresh Mail", Description: "process pending changes and sync this folder", Key: "sync", Section: "mail", Action: func() {
			statusText = "syncing..."
			go syncThreadsFromNetwork()
		}},
		{Label: "Toggle Folders", Description: "show or hide grouped labels", Key: "enter", Section: "navigation", Action: func() {
			labelsOpen = !labelsOpen
			mb.BuildFolderDisplay(labelsOpen)
			if folderSel >= mb.FolderLen() {
				folderSel = mb.FolderLen() - 1
			}
			if folderSel < 0 {
				folderSel = 0
			}
			statusText = "folders toggled"
		}},
		{Label: "Focus Folders", Description: "move focus to the folder pane", Key: "h", Section: "navigation", Action: func() {
			pane = 0
			updateFocus()
		}},
		{Label: "Focus Threads", Description: "move focus to the thread list", Key: "tab", Section: "navigation", Action: func() {
			pane = 1
			updateFocus()
		}},
		{Label: "Focus Preview", Description: "move focus to the message preview", Key: "l", Section: "navigation", Action: func() {
			pane = 2
			updateFocus()
		}},
		{Label: "Search Mail", Description: "search cached messages", Key: "/", Section: "mail", Action: startSearch},
		{Label: "Open Selected Thread", Description: "open, expand, or preview the selected row", Key: "enter", Section: "thread", Action: handleEnter},
		{Label: "Archive Selected Thread", Description: "move the selected thread out of inbox", Key: "a", Section: "thread", Action: func() {
			threadAction("archive", func() {
				pushUndo(mb.Archive(threadSel))
				clampThreadSel()
				mb.BuildFolderDisplay(labelsOpen)
				loadPreview()
				go mb.ProcessPendingCommands()
			})
		}},
		{Label: "Delete Selected Thread", Description: "move the selected thread to trash", Key: "d", Section: "thread", Action: func() {
			threadAction("delete", func() {
				pushUndo(mb.Delete(threadSel))
				clampThreadSel()
				mb.BuildFolderDisplay(labelsOpen)
				loadPreview()
				go mb.ProcessPendingCommands()
			})
		}},
		{Label: "Toggle Star", Description: "star or unstar the selected thread", Key: "s", Section: "thread", Action: func() {
			threadAction("star", func() {
				pushUndo(mb.ToggleStar(threadSel))
				go mb.ProcessPendingCommands()
			})
		}},
		{Label: "Toggle Read", Description: "mark selected thread read or unread", Key: "e", Section: "thread", Action: func() {
			threadAction("read", func() {
				pushUndo(mb.ToggleRead(threadSel))
				mb.BuildFolderDisplay(labelsOpen)
				go mb.ProcessPendingCommands()
			})
		}},
		{Label: "Undo Last Thread Action", Description: "restore the latest archive/delete/read/star change", Key: "u", Section: "thread", Action: func() {
			if len(undoStack) == 0 {
				statusText = "nothing to undo"
				return
			}
			undoStack[len(undoStack)-1]()
			undoStack = undoStack[:len(undoStack)-1]
			clampThreadSel()
			mb.BuildFolderDisplay(labelsOpen)
			loadPreview()
			statusText = "undone"
		}},
		{Label: "Show Keyboard Help", Description: "open the in-app keybinding help", Key: "?", Section: "help", Action: func() { helpOpen = true }},
		{Label: "Quit Mail", Description: "exit the app", Key: "q", Section: "system", Action: func() { app.Stop() }},
	}
	refreshOmnibox()

	fade := Animate
	accentMarker := Style{FG: t.Accent}

	// kv backs the help modal's key→description rows; IIFEs in the template
	// spread this into a ForEach for rendering.
	type kv struct{ key, desc string }

	// peakColor := Hex(0x242424)

	app.View("main",
		VBox.PaddingTRBL(1, 2, 0, 2)(
			// --- wormhole family (active focus) ---
			// If(&onWormhole).Then(ScreenEffect(ShimmerWormhole(t.BG, peakColor).Speed(&wormholeSpeed))),
			// If(&onWStreaks).Then(ScreenEffect(ShimmerWormholeWarp(t.BG, peakColor).Speed(&wormholeSpeed))),
			// If(&onWPulse).Then(ScreenEffect(ShimmerWormholeDrag(t.BG, peakColor).Speed(&wormholeSpeed))),
			// If(&onWDepth).Then(ScreenEffect(ShimmerWormholeSurge(t.BG, peakColor).Speed(&wormholeSpeed))),
			// If(&onWLayered).Then(ScreenEffect(ShimmerWormholeCore(t.BG, peakColor).Speed(&wormholeSpeed))),
			// If(&onWShear).Then(ScreenEffect(ShimmerWormholeTurbulence(t.BG, peakColor).Speed(&wormholeSpeed))),
			// If(&onWLab).Then(ScreenEffect(ShimmerWormholeLab(t.BG, peakColor).Speed(&wormholeSpeed))),
			// If(&onSilEcho).Then(ScreenEffect(ShimmerSilhouetteEcho(t.BG, peakColor))),
			// transition: capture mailbox silhouette when idle (source) and
			// fade the captured compose silhouette on return (target).
			ScreenEffect(composeTransition.SourceEffect()),
			ScreenEffect(composeTransition.TargetEffect()),
			// --- other effects (paused during wormhole iteration) ---
			// If(&onDrifting).Then(ScreenEffect(ShimmerDrifting(t.BG, peakColor))),
			// If(&onTunnel).Then(ScreenEffect(ShimmerTunnel(t.BG, peakColor).Speed(&tunnelSpeed))),
			// If(&onSweep).Then(ScreenEffect(ShimmerSweep(t.BG, peakColor))),
			// If(&onPulse).Then(ScreenEffect(ShimmerPulse(t.BG, peakColor).Trigger(&pulseTrigger))),
			// If(&onNoise).Then(ScreenEffect(ShimmerNoise(t.BG, peakColor))),
			// If(&onRain).Then(ScreenEffect(ShimmerRain(t.BG, peakColor))),
			// If(&onBreath).Then(ScreenEffect(ShimmerBreath(t.BG, peakColor))),
			// If(&onSpiral).Then(ScreenEffect(ShimmerSpiral(t.BG, peakColor))),
			// If(&onVignette).Then(ScreenEffect(ShimmerVignette(t.BG, peakColor))),
			// If(&onScatter).Then(ScreenEffect(ShimmerScatter(t.BG, peakColor))),
			SpaceH(1),
			HBox(
				Text("mail").FG(t.Bright).Bold(),
				SpaceW(2),
				Text(&statusText).FG(t.Subtle),
				SpaceW(2),
				Text("·").FG(t.Muted),
				SpaceW(2),
				Text(&shimmerLabel).FG(t.Accent).Italic(),
			),
			SpaceH(1),
			HBox.Grow(1).Gap(4)(

				VBox.Grow(1).CascadeStyle(&folderStyle)(
					HRule(), SpaceH(1),
					List(mb.FolderNames()).
						Selection(&folderSel).
						Style(fade(&folderListStyle)).
						SelectedStyle(fade(&folderSelStyle)).
						Marker("● ").MarkerStyle(accentMarker),
				),

				VBox.Grow(3).CascadeStyle(&threadStyle)(
					HRule(), SpaceH(1),
					List(mb.ThreadRows()).
						Selection(&threadSel).
						Style(fade(&threadListStyle)).
						SelectedStyle(Style{}).
						Marker("  ").
						Render(func(row *mailbox.ThreadRow) Component {
							itemBG := If(&row.Selected).Then(t.SelBG).Else(
								If(&row.Grouped).
									Then(t.GroupBG).
									Else(fade(t.BG)),
							)
							return VBox.Fill(itemBG).Border(BorderSoft).BorderFG(itemBG)(
								HBox(
									If(&row.Unread).Then(Text("●").FG(t.Accent)).Else(Text(" ")),
									SpaceW(1),
									HBox.Grow(1)(
										Text(&row.Label).Style(
											If(&row.Unread).
												Then(Style{Attr: AttrBold}).
												Else(Style{})),
										SpaceW(1),
										If(&row.Starred).Then(Text("★").FG(t.Accent)),
									),
									SpaceW(2),
									Text(&row.Date).Dim(),
								),
								HBox(
									SpaceW(2),
									Text(&row.Sender).Dim(),
									SpaceW(2),
									If(&row.HasDraft).Then(Text("draft").FG(t.Accent).Italic()),
								),
							)
						}),
				),

				VBox.Grow(3).CascadeStyle(&previewStyle)(
					HRule(), SpaceH(1),
					ScrollView.Grow(1).Ref(func(sv *ScrollViewC) {
						convView = sv
					})(
						ForEach(mb.ConversationMessages(), func(msg *mailbox.ConversationMessage) Component {
							return VBox(
								HBox(
									Text(&msg.Sender).Style(
										If(&msg.IsMe).
											Then(Style{Attr: AttrBold, FG: t.Accent}).
											Else(Style{Attr: AttrBold}),
									),
									SpaceW(1),
									Text(&msg.Date).Dim(),
								),
								SpaceH(1),
								TextBlock(&msg.Body),
								SpaceH(1),
							)
						}),
					),
				),
			),
			SpaceH(1),
			If(&omniboxOpen).Then(
				Overlay.Centered().Backdrop().BackdropFG(t.Muted)(
					VBox.
						Width(86).
						Height(&omniboxHeight).
						Fill(t.BG).
						PaddingTRBL(1, 2, 1, 2).
						Gap(1)(
						HBox(
							Text("mail").FG(t.Bright).Bold(),
							SpaceW(1),
							Text("commands").FG(t.Subtle),
							Space(),
							Text("j/k").FG(t.Muted),
							SpaceW(2),
							Text("<esc>").FG(t.Muted),
						),
						HBox.Fill(t.GroupBG).PaddingVH(0, 1)(
							Text("> ").FG(t.Accent).Bold(),
							If(&omniboxQuery).Eq("").
								Then(Text("type a command").FG(t.Muted)).
								Else(Text(&omniboxQuery).FG(t.Bright)),
						),
						ForEach(&omniboxVisible, func(cmd *mailCommand) Component {
							itemBG := If(&cmd.Selected).Then(t.SelBG).Else(t.BG)
							keyStyle := If(&cmd.Selected).
								Then(Style{FG: t.Bright, BG: t.SelBG}).
								Else(Style{FG: t.Subtle, BG: t.BG})
							return VBox.Fill(itemBG).Border(BorderSoft).BorderFG(itemBG).PaddingTRBL(0, 1, 0, 1)(
								HBox(
									Text(&cmd.Label).FG(t.Bright),
									Space(),
									Text(&cmd.Key).Style(keyStyle),
								),
								HBox(
									Text(&cmd.Section).FG(t.Accent),
									SpaceW(2),
									Text(&cmd.Description).FG(t.Subtle),
								),
							)
						}),
						If(&omniboxEmpty).Then(
							VBox.Fill(t.BG).PaddingTRBL(1, 1, 1, 1)(
								Text("no commands").FG(t.Subtle),
							),
						),
					),
				),
			),

			// help modal — ? toggles. Vignette subtly darkens the rest of
			// the screen; the modal itself is dodged so it stays crisp.
			If(&helpOpen).Then(OverlayNode{
				Centered: true,
				Child: VBox.
					Width(56).
					Fill(t.BG).
					PaddingVH(1, 2).
					NodeRef(&helpRef).
					Opacity(
						In(Animate(1.0)).Out(Animate(0)),
					).
					Gap(1)(
					Text("keyboard").FG(t.Bright).Bold(),
					HBox(
						func() Component {
							rows := []kv{
								{"j / k", "up / down"},
								{"h / l", "pane left / right"},
								{"tab", "next pane"},
								{"enter", "open"},
								{"o", "expand thread"},
								{"/", "search"},
							}
							return VBox.Grow(3)(
								Text("navigate").FG(t.Subtle),
								ForEach(&rows, func(r *kv) Component {
									return HBox.Gap(2)(Text(&r.key).FG(t.FG).Width(8), Text(&r.desc).FG(t.Subtle))
								}),
							)
						}(),
						func() Component {
							rows := []kv{
								{"c", "compose"},
								{"C", "resume draft"},
								{"r", "reply"},
								{"a", "archive"},
								{"d", "delete"},
								{"s", "star"},
								{"e", "toggle read"},
								{"u", "undo"},
							}
							return VBox.Grow(2)(
								Text("actions").FG(t.Subtle),
								ForEach(&rows, func(r *kv) Component {
									return HBox.Gap(2)(Text(&r.key).FG(t.FG).Width(3), Text(&r.desc).FG(t.Subtle))
								}),
							)
						}(),
					),
					ScreenEffect(
						SEVignette().Strength(
							In(
								Animate.From(0)(0.55),
							).Out(

								Animate(0),
							),
						).Dodge(&helpRef).Smooth(),
						SEDropShadow().Focus(&helpRef),
					),
				),
			}),
		),
	).NoCounts().
		Handle("q", app.Stop).
		Handle("?", func() { helpOpen = !helpOpen }).
		Handle("<C-p>", openOmnibox).
		Handle("<Space>", openOmnibox).
		Handle(",", func() {
			shimmerIdx = (shimmerIdx - 1 + len(variantNames)) % len(variantNames)
			// applyVariant()
			statusText = "shimmer: " + shimmerLabel
		}).
		Handle(".", func() {
			shimmerIdx = (shimmerIdx + 1) % len(variantNames)

			statusText = "shimmer: " + shimmerLabel
		}).
		// number keys set wormhole speed — 0 = stopped, 9 = hyperspeed
		// Handle("0", func() { wormholeSpeed = 0; statusText = "speed: 0" }).
		// Handle("1", func() { wormholeSpeed = 2; statusText = "speed: 2" }).
		// Handle("2", func() { wormholeSpeed = 4; statusText = "speed: 4" }).
		// Handle("3", func() { wormholeSpeed = 6; statusText = "speed: 6" }).
		// Handle("4", func() { wormholeSpeed = 8; statusText = "speed: 8" }).
		// Handle("5", func() { wormholeSpeed = 10; statusText = "speed: 10" }).
		// Handle("6", func() { wormholeSpeed = 12; statusText = "speed: 12" }).
		// Handle("7", func() { wormholeSpeed = 15; statusText = "speed: 15" }).
		// Handle("8", func() { wormholeSpeed = 18; statusText = "speed: 18" }).
		// Handle("9", func() { wormholeSpeed = 22; statusText = "speed: 22 (hyper)" }).
		Handle("j", func() {
			switch pane {
			case 0:
				if folderSel < mb.FolderLen()-1 {
					folderSel++
					loadFolder()
				}
			case 1:
				if threadSel < mb.ThreadLen()-1 {
					threadSel++
					mb.SetSelected(threadSel)
					loadPreview()
				}
			case 2:
				if convView != nil {
					convView.Layer().ScrollDown(1)
				}
			}
		}).
		Handle("k", func() {
			switch pane {
			case 0:
				if folderSel > 0 {
					folderSel--
					loadFolder()
				}
			case 1:
				if threadSel > 0 {
					threadSel--
					mb.SetSelected(threadSel)
					loadPreview()
				}
			case 2:
				if convView != nil {
					convView.Layer().ScrollUp(1)
				}
			}
		}).
		Handle("l", func() {
			if pane < 2 {
				pane++
				updateFocus()
			}
		}).
		Handle("h", func() {
			if pane > 0 {
				pane--
				updateFocus()
			}
		}).
		Handle("<Tab>", func() {
			pane = (pane + 1) % 3
			updateFocus()
		}).
		Handle("<S-Tab>", func() {
			pane = (pane + 2) % 3
			updateFocus()
		}).
		Handle("<Enter>", func() {
			switch pane {
			case 0:
				if folderSel == mb.CanonEnd() {
					labelsOpen = !labelsOpen
					mb.BuildFolderDisplay(labelsOpen)
					break
				}
				pane = 1
				updateFocus()
			case 1:
				handleEnter()
			}
		}).
		Handle("<Escape>", func() {
			if helpOpen {
				helpOpen = false
				return
			}
			if pane > 0 {
				pane--
				updateFocus()
			}
		}).
		Handle("o", func() {
			if pane == 1 {
				mb.ToggleThread(threadSel)
			}
		}).
		Handle("c", func() { comp.Open() }).
		Handle("C", func() { comp.ResumeLast() }).
		Handle("r", func() {
			if t := mb.SelectedThread(threadSel); t != nil {
				if row := mb.ThreadRowAt(threadSel); row != nil && row.MsgIdx < 0 {
					// Drafts folder: r resumes the draft (muscle-memory
					// consistency with Enter) rather than starting a
					// nonsensical reply to your own draft.
					if mb.ActiveFolderCanonical() == "Drafts" {
						comp.ResumeDraft(t.ID)
						return
					}
					comp.Open()
					comp.SetupReply(*t)
				}
			}
		}).
		Handle("a", func() {
			if pane == 1 {
				pushUndo(mb.Archive(threadSel))
				clampThreadSel()
				mb.BuildFolderDisplay(labelsOpen)
				go mb.ProcessPendingCommands()
			}
		}).
		Handle("d", func() {
			if pane == 1 {
				pushUndo(mb.Delete(threadSel))
				clampThreadSel()
				mb.BuildFolderDisplay(labelsOpen)
				go mb.ProcessPendingCommands()
			}
		}).
		Handle("s", func() {
			if pane == 1 {
				pushUndo(mb.ToggleStar(threadSel))
				go mb.ProcessPendingCommands()
			}
		}).
		Handle("e", func() {
			if pane == 1 {
				pushUndo(mb.ToggleRead(threadSel))
				mb.BuildFolderDisplay(labelsOpen)
				go mb.ProcessPendingCommands()
			}
		}).
		Handle("u", func() {
			if pane == 1 && len(undoStack) > 0 {
				undoStack[len(undoStack)-1]()
				undoStack = undoStack[:len(undoStack)-1]
				clampThreadSel()
				mb.BuildFolderDisplay(labelsOpen)
				loadPreview()
				if len(undoStack) > 0 {
					statusText = fmt.Sprintf("%d undoable — u to undo", len(undoStack))
				} else {
					statusText = "undone"
				}
			}
		}).
		Handle("/", func() {
			startSearch()
		})

	execOmnibox := func() {
		if omniboxSel < 0 || omniboxSel >= len(omniboxFiltered) {
			return
		}
		action := omniboxFiltered[omniboxSel].Action
		closeOmnibox()
		if action != nil {
			action()
		}
	}

	omniboxRouter = riffkey.NewRouter().Name("omnibox").NoCounts()
	omniboxRouter.Handle("<CR>", func(_ riffkey.Match) { execOmnibox() })
	omniboxRouter.Handle("<Enter>", func(_ riffkey.Match) { execOmnibox() })
	omniboxRouter.Handle("<Esc>", func(_ riffkey.Match) { closeOmnibox() })
	omniboxRouter.Handle("<C-c>", func(_ riffkey.Match) { closeOmnibox() })
	omniboxRouter.Handle("j", func(_ riffkey.Match) { moveOmnibox(1) })
	omniboxRouter.Handle("<Down>", func(_ riffkey.Match) { moveOmnibox(1) })
	omniboxRouter.Handle("<Tab>", func(_ riffkey.Match) { moveOmnibox(1) })
	omniboxRouter.Handle("<C-n>", func(_ riffkey.Match) { moveOmnibox(1) })
	omniboxRouter.Handle("k", func(_ riffkey.Match) { moveOmnibox(-1) })
	omniboxRouter.Handle("<Up>", func(_ riffkey.Match) { moveOmnibox(-1) })
	omniboxRouter.Handle("<S-Tab>", func(_ riffkey.Match) { moveOmnibox(-1) })
	omniboxRouter.Handle("<C-p>", func(_ riffkey.Match) { moveOmnibox(-1) })
	omniboxRouter.Handle("<C-d>", func(_ riffkey.Match) { pageOmnibox(5) })
	omniboxRouter.Handle("<C-u>", func(_ riffkey.Match) { pageOmnibox(-5) })
	omniboxRouter.Handle("g", func(_ riffkey.Match) {
		omniboxSel = 0
		refreshOmniboxSelection()
	})
	omniboxRouter.Handle("G", func(_ riffkey.Match) {
		if len(omniboxFiltered) > 0 {
			omniboxSel = len(omniboxFiltered) - 1
		}
		refreshOmniboxSelection()
	})
	omniboxRouter.Handle("<BS>", func(_ riffkey.Match) {
		if len(omniboxQuery) > 0 {
			runes := []rune(omniboxQuery)
			omniboxQuery = string(runes[:len(runes)-1])
			omniboxSel = 0
			refreshOmnibox()
		}
	})
	omniboxRouter.Handle("<Space>", func(_ riffkey.Match) {
		omniboxQuery += " "
		omniboxSel = 0
		refreshOmnibox()
	})
	omniboxRouter.HandleUnmatched(func(k riffkey.Key) bool {
		if k.Rune != 0 && k.Mod == 0 {
			omniboxQuery += string(k.Rune)
			omniboxSel = 0
			refreshOmnibox()
			app.RequestRender()
			return true
		}
		return false
	})

	app.View("search",
		VBox(
			HBox(
				Text("/").FG(t.Bright).Bold(),
				Text(&searchQuery),
			),
		),
	).
		Handle("<CR>", func() {
			q := searchQuery
			searchQuery = ""
			app.ShowCursor()
			app.PopView()
			if q == "" {
				return
			}
			results, err := db.Search(q, 50)
			if err != nil {
				statusText = fmt.Sprintf("search: %v", err)
				return
			}
			mb.SetSearchResults(results)
			mb.BuildThreadDisplay()
			threadSel = 0
			mb.SetSelected(0)
			pane = 1
			updateFocus()
			statusText = fmt.Sprintf("search: %q (%d)", q, len(results))
		}).
		Handle("<Esc>", func() {
			searchQuery = ""
			app.ShowCursor()
			app.PopView()
		}).
		Handle("<BS>", func() {
			if len(searchQuery) > 0 {
				runes := []rune(searchQuery)
				searchQuery = string(runes[:len(runes)-1])
			}
		}).
		NoCounts()

	if searchRouter, ok := app.ViewRouter("search"); ok {
		searchRouter.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				searchQuery += string(k.Rune)
				app.RequestRender()
				return true
			}
			return false
		})
	}

	// spinner
	go func() {
		for range time.Tick(80 * time.Millisecond) {
			frame++
			app.RequestRender()
		}
	}()

	if err := app.RunFrom("main"); err != nil {
		log.Fatal(err)
	}
}

type composeControls struct {
	Open        func()
	SetupReply  func(provider.Thread)
	ResumeLast  func()
	ResumeDraft func(threadID string)
}

func composeCursorColor(ed *compose.Editor) Color {
	t := ed.Theme()
	switch ed.Mode() {
	case compose.ModeInsert:
		return t.Cursor.Insert
	case compose.ModeVisual:
		return t.Cursor.Visual
	default:
		return t.Cursor.Normal
	}
}

func setupComposeView(app *App, ed *compose.Editor, mb *mailbox.Mailbox, smtp *smtpprov.SMTP, db *cache.Cache, statusText *string, frame *int, transition *viewTransition, theme AppTheme) composeControls {
	// compose data
	var to, cc, subject string
	var replyMsg *provider.Message

	// compose view state
	var fieldTo, fieldCC, fieldSubject InputState
	var fieldFocus FocusGroup
	var focused bool
	labelTo, labelCC, labelSub := theme.Muted, theme.Muted, theme.Muted
	var toFieldRef, ccFieldRef NodeRef
	var contactResults []string
	var contactSel int
	var showContacts bool
	var showSending bool
	var sendingStatus string
	// tracks whether compose is the active view. the compose router's
	// AddOnAfter hook refreshes the editor after every keypress, which calls
	// updateCursor → app.ShowCursor. when a handler exits compose (e.g. <Esc>
	// → exitCompose), the afterHook still fires and re-enables the terminal
	// cursor on top of the mailbox view. guarding on this bool prevents that.
	var composeActive bool

	// compose search state
	var searchQuery, searchPrompt string
	var searchFwd bool

	// draft state — thread_id the composer is editing ("" = new compose).
	// scheduleDraftSave debounces cache writes so we're not hitting sqlite
	// on every keystroke; the timer is reset on every keypress. draftTouched
	// tracks whether this session saw any edits — without it, exiting a
	// never-touched fresh compose would "save" an empty state and silently
	// delete any pre-existing "" draft row.
	var currentDraftID string
	var draftSaveTimer *time.Timer
	var draftTouched bool

	// pendingCursorShow: set true on every compose entry. While the view
	// transition is still fading in, the real terminal cursor stays hidden
	// and transition.TargetEffect paints a fake cursor into the buffer. Once
	// the transition completes, Refresh hands back to the real cursor.
	var pendingCursorShow bool

	transition.CursorOverlay(func() (int, int, Color, bool) {
		if !composeActive || focused {
			return 0, 0, Color{}, false
		}
		x, y := ed.CursorScreenPos()
		return x, y, composeCursorColor(ed), true
	})

	reset := func() {
		to, cc, subject = "", "", ""
		replyMsg = nil
		fieldTo.Clear()
		fieldCC.Clear()
		fieldSubject.Clear()
		fieldFocus.Current = -1
		focused = false
		ed.ResetDocument(compose.NewDocument())
		currentDraftID = ""
		draftTouched = false
	}

	snapshotDraft := func() cache.Draft {
		var body string
		if ed != nil && ed.Doc() != nil {
			var sb strings.Builder
			_ = compose.WriteMarkdown(ed.Doc(), &sb)
			body = sb.String()
		}
		// read directly from field state — outer to/cc/subject only sync
		// on field exit, so snapshotting them would miss edits still
		// in-flight while the user is typing into a field.
		return cache.Draft{
			ThreadID: currentDraftID,
			To:       fieldTo.Value,
			Cc:       fieldCC.Value,
			Subject:  fieldSubject.Value,
			Body:     body,
		}
	}

	saveDraft := func() {
		if db == nil {
			return
		}
		d := snapshotDraft()
		bodyLen := len(d.Body)
		if err := db.PutDraft(d); err != nil {
			log.Printf("saveDraft: PutDraft failed: %v", err)
			return
		}
		log.Printf("saveDraft: wrote thread=%q subject=%q bodyLen=%d isEmpty=%v", d.ThreadID, d.Subject, bodyLen, d.IsEmpty())
	}

	scheduleDraftSave := func() {
		if !composeActive || db == nil {
			return
		}
		draftTouched = true
		if draftSaveTimer != nil {
			draftSaveTimer.Stop()
		}
		draftSaveTimer = time.AfterFunc(1*time.Second, saveDraft)
	}

	loadDraft := func(threadID string) bool {
		if db == nil {
			return false
		}
		d, found, err := db.GetDraft(threadID)
		if err != nil || !found {
			return false
		}
		to = d.To
		cc = d.Cc
		subject = d.Subject
		fieldTo.Value = d.To
		fieldTo.Cursor = len(d.To)
		fieldCC.Value = d.Cc
		fieldCC.Cursor = len(d.Cc)
		fieldSubject.Value = d.Subject
		fieldSubject.Cursor = len(d.Subject)
		ed.ResetDocument(compose.ParseMarkdown(d.Body))
		return true
	}

	send := func() {
		log.Printf("sendMessage: to=%q cc=%q subject=%q", to, cc, subject)
		if smtp == nil {
			log.Println("sendMessage: no smtp configured")
			return
		}
		msg := provider.Message{
			To:       parseRecipients(to),
			CC:       parseRecipients(cc),
			Subject:  subject,
			HTMLBody: ed.ToHTML(),
			TextBody: ed.ToPlainText(),
		}
		if replyMsg != nil {
			msg.InReplyTo = replyMsg.MessageID
			msg.References = append(replyMsg.References, replyMsg.MessageID)
		}
		showSending = true
		sendingStatus = "sending..."
		app.RequestRender()

		go func() {
			log.Println("sendMessage: sending via smtp...")
			err := smtp.Send(&msg)
			showSending = false
			if err != nil {
				log.Printf("sendMessage: failed: %v", err)
				*statusText = fmt.Sprintf("send failed: %v", err)
				app.RequestRender()
				return
			}
			// save sent message metadata to cache for threading
			msg.Date = time.Now()
			msg.Read = true
			if db != nil {
				db.PutSentMessage(msg)
				// DeleteDraft queues a delete_draft command if there was a
				// remote copy; flush it now so the server-side draft goes
				// away in the same moment as the send.
				db.DeleteDraft(currentDraftID)
				go mb.ProcessPendingCommands()
			}
			log.Printf("sendMessage: sent to %s (msgid=%s)", to, msg.MessageID)
			*statusText = fmt.Sprintf("sent to %s", to)
			composeActive = false
			reset()
			app.HideCursor()
			app.Go("main")
			app.RequestRender()
		}()
	}

	// wire enterInsertMode for operator text object combos
	var enterInsertMode func()
	compose.SetEnterInsertMode(func(a *App, e *compose.Editor) {
		enterInsertMode()
	})

	enterInsertMode = func() {
		syncSubject := func() {
			b := ed.CurrentBlock()
			if b == nil {
				return
			}
			switch b.Type {
			case compose.BlockH1, compose.BlockH2, compose.BlockH3:
				subject = b.Text()
				fieldSubject.Value = subject
				fieldSubject.Cursor = len(subject)
			}
		}

		compose.RegisterInsertMode(app, ed, syncSubject)

		if r := app.Router(); r != nil {
			r.Handle("<C-s>", func(_ riffkey.Match) {
				ed.EnterNormal()
				app.Pop()
				if to != "" {
					send()
				}
			})
		}
	}

	// compose view layout
	app.View("compose",
		VBox(
			// symmetric: source captures compose silhouette when idle so the
			// return transition (compose → mailbox) has something to fade out.
			ScreenEffect(transition.SourceEffect()),
			ScreenEffect(transition.TargetEffect()),
			LayerView(ed.Layer()).Grow(1),

			VBox(
				SpaceH(1),
				HBox(Space(), VBox.Width(64)(
					HBox.Gap(1).NodeRef(&toFieldRef)(
						Text("TO").FG(&labelTo),
						TextInput{Field: &fieldTo, FocusGroup: &fieldFocus, FocusIndex: 0,
							Placeholder: "·····", PlaceholderStyle: Style{Attr: AttrDim}},
					),
					HBox.Gap(1).NodeRef(&ccFieldRef)(
						Text("CC").FG(&labelCC),
						TextInput{Field: &fieldCC, FocusGroup: &fieldFocus, FocusIndex: 1,
							Placeholder: "·····", PlaceholderStyle: Style{Attr: AttrDim}},
					),
					HBox.Gap(1)(
						Text("SUBJECT").FG(&labelSub),
						TextInput{Field: &fieldSubject, FocusGroup: &fieldFocus, FocusIndex: 2,
							Placeholder: "·····", PlaceholderStyle: Style{Attr: AttrDim}},
					),
				), Space()),
				SpaceH(1),
			),

			If(&showContacts).Then(
				Overlay.Above(&toFieldRef)(
					VBox.Border(BorderRounded).BorderFG(theme.Muted)(
						List(&contactResults).
							Selection(&contactSel).
							SelectedStyle(Style{Attr: AttrInverse}).
							MaxVisible(6),
					),
				),
			),

			If(&showSending).Then(
				Overlay.Centered().Backdrop().BackdropFG(theme.BG)(
					VBox.Border(BorderRounded).BorderFG(theme.Muted).Width(40)(
						SpaceH(1),
						HBox(
							Space(),
							Spinner(frame).Frames(SpinnerDots).FG(theme.Subtle),
							SpaceW(1),
							Text(&sendingStatus).Style(Style{Align: AlignCenter}),
							Space(),
						),
						SpaceH(1),
					),
				),
			),
		),
	).NoCounts()

	// editor render callback
	ed.Layer().Render = func() {
		w := ed.Layer().ViewportWidth()
		h := ed.Layer().ViewportHeight()
		if w > 0 && h > 0 {
			ed.SetSize(w, h)
			if pendingCursorShow {
				if transition.Complete() {
					pendingCursorShow = false
					ed.Refresh()
				} else {
					ed.UpdateDisplay()
					app.HideCursor()
				}
			} else {
				ed.UpdateDisplay()
			}
		}
	}
	ed.Layer().AlwaysRender = true

	// heartbeat for long compose sessions — safety net that flushes the
	// current state to server every 60s of continuous editing, so a crash
	// mid-draft doesn't lose material written between commit points.
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if !composeActive || !draftTouched {
				continue
			}
			saveDraft()
			mb.ProcessPendingCommands()
		}
	}()

	// compose search view
	app.View("compose-search",
		VBox(
			HBox(
				Text(&searchPrompt).Bold(),
				Text(&searchQuery),
			),
		),
	).
		Handle("<CR>", func() {
			q := searchQuery
			fwd := searchFwd
			searchQuery = ""
			app.ShowCursor()
			app.PopView()
			if q != "" {
				ed.Search(q, fwd)
			}
			ed.Refresh()
		}).
		Handle("<Esc>", func() {
			searchQuery = ""
			app.ShowCursor()
			app.PopView()
			ed.Refresh()
		}).
		Handle("<BS>", func() {
			if len(searchQuery) > 0 {
				runes := []rune(searchQuery)
				searchQuery = string(runes[:len(runes)-1])
			}
		}).
		NoCounts()

	if searchRouter, ok := app.ViewRouter("compose-search"); ok {
		searchRouter.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				searchQuery += string(k.Rune)
				app.RequestRender()
				return true
			}
			return false
		})
	}

	// keybindings
	if router, ok := app.ViewRouter("compose"); ok {
		exitCompose := func() {
			// final synchronous draft snapshot before tearing the view down —
			// any pending debounced save is now moot. skip entirely if the
			// session saw no edits so we don't clobber an existing draft with
			// an empty-state delete.
			if draftSaveTimer != nil {
				draftSaveTimer.Stop()
			}
			if draftTouched {
				saveDraft()
				// flush any queued draft-sync commands so the server-side
				// Drafts folder reflects this session as the user leaves.
				go mb.ProcessPendingCommands()
			}
			composeActive = false
			reset()
			app.HideCursor()
			transition.Start()
			app.Go("main")
		}

		router.Handle("<C-q>", func(_ riffkey.Match) { exitCompose() })

		router.Handle("<Esc>", func(_ riffkey.Match) {
			ed.ExitDialogueIfEmpty()
			exitCompose()
		})

		// send panel
		fieldStates := []*InputState{&fieldTo, &fieldCC, &fieldSubject}
		labels := []*Color{&labelTo, &labelCC, &labelSub}

		syncLabels := func() {
			for i, l := range labels {
				if focused && fieldFocus.Current == i {
					*l = theme.Bright
				} else {
					*l = theme.Muted
				}
			}
		}

		var lastContactQuery string

		searchContacts := func() {
			if fieldFocus.Current > 1 {
				showContacts = false
				return
			}
			query := fieldStates[fieldFocus.Current].Value
			if query == lastContactQuery {
				return
			}
			lastContactQuery = query
			if len(query) < 2 {
				showContacts = false
				contactResults = nil
				return
			}
			go func() {
				var results []provider.Address
				if db != nil {
					results, _ = db.SearchContacts(query)
				}
				contactResults = nil
				for _, r := range results {
					contactResults = append(contactResults, r.String())
				}
				contactSel = 0
				showContacts = len(contactResults) > 0
				app.RequestRender()
			}()
		}

		bindCurrentField := func(fr *riffkey.Router) {
			f := fieldStates[fieldFocus.Current]
			th := riffkey.NewTextHandler(&f.Value, &f.Cursor)
			fr.HandleUnmatched(func(k riffkey.Key) bool {
				handled := th.HandleKey(k)
				if handled {
					searchContacts()
					// typing in To/Cc/Subject must mark the draft dirty too
					// — without this, header-only edits never trigger
					// scheduleDraftSave and never reach the server.
					scheduleDraftSave()
				}
				return handled
			})
			fr.NoCounts()
			syncLabels()
			showContacts = false
			lastContactQuery = ""
		}

		exitFields := func() {
			to = fieldTo.Value
			cc = fieldCC.Value
			subject = fieldSubject.Value
			fieldFocus.Current = -1
			focused = false
			showContacts = false
			syncLabels()
			app.Pop()
			ed.Refresh()
		}

		router.Handle("<Tab>", func(_ riffkey.Match) {
			focused = true
			fieldFocus.Current = 0
			app.HideCursor()

			fr := riffkey.NewRouter().Name("send-fields")
			bindCurrentField(fr)

			fr.Handle("<Tab>", func(_ riffkey.Match) {
				next := fieldFocus.Current + 1
				if next >= len(fieldStates) {
					exitFields()
					return
				}
				fieldFocus.Current = next
				bindCurrentField(fr)
			})
			fr.Handle("<S-Tab>", func(_ riffkey.Match) {
				prev := fieldFocus.Current - 1
				if prev < 0 {
					exitFields()
					return
				}
				fieldFocus.Current = prev
				bindCurrentField(fr)
			})
			fr.Handle("<Esc>", func(_ riffkey.Match) {
				if showContacts {
					showContacts = false
					return
				}
				exitFields()
			})
			fr.Handle("<C-s>", func(_ riffkey.Match) {
				exitFields()
				if to != "" {
					send()
				}
			})
			fr.Handle("<CR>", func(_ riffkey.Match) {
				if showContacts && contactSel >= 0 && contactSel < len(contactResults) {
					fieldStates[fieldFocus.Current].Value = contactResults[contactSel]
					fieldStates[fieldFocus.Current].Cursor = len(contactResults[contactSel])
					showContacts = false
					return
				}
				next := fieldFocus.Current + 1
				if next >= len(fieldStates) {
					exitFields()
					return
				}
				fieldFocus.Current = next
				bindCurrentField(fr)
			})
			fr.Handle("<Down>", func(_ riffkey.Match) {
				if showContacts && contactSel < len(contactResults)-1 {
					contactSel++
				}
			})
			fr.Handle("<Up>", func(_ riffkey.Match) {
				if showContacts && contactSel > 0 {
					contactSel--
				}
			})

			fr.AddOnAfter(func() { app.RequestRender() })
			app.Push(fr)
		})

		router.Handle("<C-s>", func(_ riffkey.Match) {
			if to != "" {
				send()
			}
		})
		router.Handle(":send<CR>", func(_ riffkey.Match) {
			if to != "" {
				send()
			}
		})
		router.Handle(":s<CR>", func(_ riffkey.Match) {
			if to != "" {
				send()
			}
		})

		// search
		composeStartSearch := func(forward bool) {
			if forward {
				searchPrompt = "/"
			} else {
				searchPrompt = "?"
			}
			searchQuery = ""
			searchFwd = forward
			app.HideCursor()
			app.PushView("compose-search")
		}
		router.Handle("/", func(_ riffkey.Match) { composeStartSearch(true) })
		router.Handle("?", func(_ riffkey.Match) { composeStartSearch(false) })

		// pure editor keybindings
		compose.RegisterNormalMode(router, app, ed,
			func() { enterInsertMode() },
			func() { compose.RegisterVisualMode(app, ed) },
		)

		router.AddOnAfter(func() {
			if !composeActive {
				return
			}
			ed.Refresh()
			if pendingCursorShow && !transition.Complete() {
				app.HideCursor()
			}
			if focused {
				app.HideCursor()
			}
			scheduleDraftSave()
		})
	}

	return composeControls{
		Open: func() {
			reset()
			// Give each new compose its own stable thread_id so multiple
			// `c` sessions produce independent cache rows and independent
			// server drafts. Random hex (not UID-shaped) so the drafts
			// projection in the mailbox view can distinguish locally-owned
			// ids from anything that might have leaked in from IMAP.
			id, err := newComposeID()
			if err != nil {
				log.Printf("Open: newComposeID failed: %v", err)
				return
			}
			currentDraftID = id
			ed.SetTypewriterMode(true)
			composeActive = true
			pendingCursorShow = true
			// c is always a fresh canvas — never auto-loads a saved draft.
			// To resume the last draft (any kind), the user presses C.
			transition.Start()
			app.Go("compose")
			// no ed.Refresh() here — screen dimensions aren't set yet, so
			// updateCursor would emit SetCursor(0,0)+ShowCursor before the
			// first render, leaking a terminal cursor on the mailbox side
			// of the transition. Layer.prepare() triggers UpdateDisplay on
			// the first compose render, and key handlers call updateCursor
			// themselves when they move the cursor.
		},
		SetupReply: func(thread provider.Thread) {
			currentDraftID = thread.ID
			lastMsg := thread.Messages[len(thread.Messages)-1]
			replyMsg = &lastMsg

			// resume the thread's draft if one is already saved.
			if loadDraft(thread.ID) {
				return
			}

			to = lastMsg.From.String()
			s := lastMsg.Subject
			if !strings.HasPrefix(strings.ToLower(s), "re:") {
				s = "Re: " + s
			}
			subject = s

			// build document with quoted original text
			body := lastMsg.TextBody
			if body == "" && lastMsg.HTMLBody != "" {
				body = lastMsg.HTMLBody // will be plain enough for quoting
			}
			doc := compose.NewDocument()
			doc.Blocks = []compose.Block{
				{Type: compose.BlockParagraph, Runs: []compose.Run{{Text: ""}}},
				{Type: compose.BlockParagraph, Runs: []compose.Run{{Text: ""}}},
			}
			for _, line := range strings.Split(body, "\n") {
				doc.Blocks = append(doc.Blocks, compose.Block{
					Type: compose.BlockQuote,
					Runs: []compose.Run{{Text: line}},
				})
			}
			ed.ResetDocument(doc)
		},
		ResumeLast: func() {
			if db == nil {
				return
			}
			d, found, err := db.GetLastDraft()
			if err != nil || !found {
				*statusText = "no drafts to resume"
				app.RequestRender()
				return
			}
			reset()
			currentDraftID = d.ThreadID
			to = d.To
			cc = d.Cc
			subject = d.Subject
			fieldTo.Value = d.To
			fieldTo.Cursor = len(d.To)
			fieldCC.Value = d.Cc
			fieldCC.Cursor = len(d.Cc)
			fieldSubject.Value = d.Subject
			fieldSubject.Cursor = len(d.Subject)
			ed.ResetDocument(compose.ParseMarkdown(d.Body))

			// for reply drafts, re-hydrate replyMsg from cache so send still
			// produces correct In-Reply-To / References headers.
			if d.ThreadID != "" {
				if t, err := db.GetThread(d.ThreadID); err == nil && len(t.Messages) > 0 {
					lastMsg := t.Messages[len(t.Messages)-1]
					replyMsg = &lastMsg
				}
			}

			ed.SetTypewriterMode(true)
			composeActive = true
			pendingCursorShow = true
			transition.Start()
			app.Go("compose")
		},
		ResumeDraft: func(threadID string) {
			// Drafts folder is a projection of the drafts table, so the
			// thread id we were handed IS the local draft id — read from
			// the table and we're always in sync with the latest save.
			if db == nil {
				return
			}
			d, found, err := db.GetDraft(threadID)
			if err != nil || !found {
				*statusText = "draft not found"
				app.RequestRender()
				return
			}
			reset()
			currentDraftID = d.ThreadID
			to = d.To
			cc = d.Cc
			subject = d.Subject
			fieldTo.Value = d.To
			fieldTo.Cursor = len(d.To)
			fieldCC.Value = d.Cc
			fieldCC.Cursor = len(d.Cc)
			fieldSubject.Value = d.Subject
			fieldSubject.Cursor = len(d.Subject)
			ed.ResetDocument(compose.ParseMarkdown(d.Body))

			// Reply drafts share a thread id with the parent conversation —
			// rehydrate replyMsg from that thread so send restores the
			// In-Reply-To / References headers. New-compose and adopted
			// server drafts don't match any thread; harmless miss.
			if t, err := db.GetThread(d.ThreadID); err == nil && len(t.Messages) > 0 {
				lastMsg := t.Messages[len(t.Messages)-1]
				replyMsg = &lastMsg
			}

			ed.SetTypewriterMode(true)
			composeActive = true
			pendingCursorShow = true
			transition.Start()
			app.Go("compose")
		},
	}
}

func formatAddressList(addrs []provider.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}

func parseRecipients(s string) []provider.Address {
	if s == "" {
		return nil
	}
	var addrs []provider.Address
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.LastIndex(part, "<"); idx >= 0 {
			name := strings.TrimSpace(part[:idx])
			email := strings.TrimRight(part[idx+1:], ">")
			email = strings.TrimSpace(email)
			addrs = append(addrs, provider.Address{Name: name, Email: email})
		} else {
			addrs = append(addrs, provider.Address{Email: part})
		}
	}
	return addrs
}

func cacheContacts(db *cache.Cache) {
	if db == nil {
		return
	}
	log.Println("contacts: loading from macOS...")
	all := contacts.All()
	log.Printf("contacts: loaded %d, caching", len(all))
	if len(all) > 0 {
		db.PutContacts(all)
	}
}
