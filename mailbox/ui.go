package mailbox

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/provider"
	"github.com/kungfusheep/mail/theme"
	"github.com/kungfusheep/mail/ui"
	"github.com/kungfusheep/riffkey"
)

const (
	FolderPane = iota
	ThreadPane
	PreviewPane
)

type UIConfig struct {
	App   *glyph.App
	Cache *cache.Cache
	State *State
	Theme theme.Theme
}

type ComposeControls struct {
	Open            func()
	SetupReply      func(provider.Thread)
	SetupReplyAll   func(provider.Thread)
	SetupForward    func(provider.Thread)
	OpenInlineReply func(provider.Thread)
	InlineView      func(*glyph.NodeRef) glyph.Component
	ResumeLast      func()
	ResumeDraft     func(threadID string)
}

type undoItem struct {
	run     func()
	message string
}

type UI struct {
	App       *glyph.App
	Cache     *cache.Cache
	State     *State
	Theme     theme.Theme
	ThemeName string
	Compose   ComposeControls

	WatchActiveFolder func()
	SyncActiveFolder  func()

	FolderSel            int
	ThreadSel            int
	LabelsOpen           bool
	HelpOpen             bool
	HelpRef              glyph.NodeRef
	FolderPaneRef        glyph.NodeRef
	ThreadPaneRef        glyph.NodeRef
	PreviewPaneRef       glyph.NodeRef
	Frame                int
	FolderTitle          string
	SearchQuery          string
	ThreadUnreadText     string
	StatusVisible        bool
	Pane                 int
	PreviewCanScroll     bool
	PreviewScrollVisible bool
	PreviewScrollPos     int
	PreviewScrollTrack   glyph.Style
	PreviewScrollThumb   glyph.Style
	BG                   glyph.Color
	Bright               glyph.Color
	FG                   glyph.Color
	Subtle               glyph.Color
	Dim                  glyph.Color
	Muted                glyph.Color
	Accent               glyph.Color
	Info                 glyph.Color
	Success              glyph.Color
	Warning              glyph.Color
	Error                glyph.Color
	SelBG                glyph.Color
	GroupBG              glyph.Color
	ThreadBG             glyph.Color

	FolderStyle     glyph.Style
	ThreadStyle     glyph.Style
	PreviewStyle    glyph.Style
	FolderListStyle glyph.Style
	FolderSelStyle  glyph.Style
	ThreadListStyle glyph.Style

	ConvView *glyph.ScrollViewC

	statusFeed *ui.Feed
	undoStack  []undoItem

	pendingThreadsChanged atomic.Bool
}

func NewUI(cfg UIConfig) *UI {
	m := &UI{
		App:         cfg.App,
		Cache:       cfg.Cache,
		State:       cfg.State,
		Theme:       cfg.Theme,
		ThemeName:   "dark",
		FolderTitle: "Inbox",
		Pane:        ThreadPane,
		statusFeed:  ui.NewFeed(time.Now),
	}
	m.ApplyTheme(cfg.Theme)
	m.UpdateThreadHeader()
	m.UpdateStatusOverlay()
	return m
}

func (m *UI) ApplyTheme(t theme.Theme) {
	m.Theme = t
	m.BG = t.BG
	m.Bright = t.Bright
	m.FG = t.FG
	m.Subtle = t.Subtle
	m.Dim = t.Dim
	m.Muted = t.Muted
	m.Accent = t.Accent
	m.Info = t.Info
	m.Success = t.Success
	m.Warning = t.Warning
	m.Error = t.Error
	m.SelBG = t.SelBG
	m.GroupBG = t.GroupBG
	m.ThreadBG = t.ThreadBG
	m.PreviewScrollTrack = glyph.Style{FG: t.Muted}
	m.PreviewScrollThumb = glyph.Style{FG: t.Subtle}
	if m.App != nil {
		m.App.SetDefaultStyle(glyph.Style{FG: t.FG, BG: t.BG})
	}
	if m.ConvView != nil {
		m.ConvView.Layer().Invalidate()
	}
	m.UpdateFocus()
}

func (m *UI) SetCompose(comp ComposeControls) {
	m.Compose = comp
}

func (m *UI) SetRuntime(watchActiveFolder, syncActiveFolder func()) {
	m.WatchActiveFolder = watchActiveFolder
	m.SyncActiveFolder = syncActiveFolder
}

func (m *UI) SetConversationView(sv *glyph.ScrollViewC) {
	m.ConvView = sv
}

func (m *UI) StatusItems() *[]ui.Notification {
	return m.statusFeed.Items()
}

func (m *UI) UpdateStatusOverlay() {
	m.statusFeed.Update()
	m.StatusVisible = len(*m.statusFeed.Items()) > 0
}

func (m *UI) UpdatePreviewScroll() {
	m.PreviewCanScroll = false
	m.PreviewScrollVisible = false
	m.PreviewScrollPos = 0
	if m.ConvView == nil {
		return
	}
	layer := m.ConvView.Layer()
	maxScroll := layer.MaxScroll()
	viewHeight := layer.ViewportHeight()
	contentHeight := layer.ContentHeight()
	if maxScroll <= 0 || viewHeight <= 0 || contentHeight <= viewHeight {
		return
	}
	m.PreviewCanScroll = true
	m.PreviewScrollVisible = m.Pane == PreviewPane
	m.PreviewScrollPos = (layer.ScrollY() * 100) / maxScroll
}

func (m *UI) Notify(text string) {
	m.NotifyKind(ui.NotificationInfo, text)
}

func (m *UI) NotifyKind(kind ui.NotificationKind, text string) {
	m.statusFeed.PushKind(kind, text)
	m.UpdateStatusOverlay()
	m.App.RequestRender()
}

func (m *UI) NotifySuccess(text string) {
	m.NotifyKind(ui.NotificationSuccess, text)
}

func (m *UI) NotifyWarning(text string) {
	m.NotifyKind(ui.NotificationWarning, text)
}

func (m *UI) NotifyError(text string) {
	m.NotifyKind(ui.NotificationError, text)
}

func (m *UI) NotifyAction(text string) {
	m.NotifyKind(ui.NotificationAction, text)
}

func (m *UI) NotifyRuntime(text string) {
	switch {
	case strings.HasPrefix(text, "imap: "), strings.HasPrefix(text, "sync: "):
		m.NotifyError(text)
	case text == "synced", text == "cache refreshed":
		m.NotifySuccess(text)
	default:
		m.Notify(text)
	}
}

func (m *UI) NotifyCompose(text string) {
	switch {
	case strings.HasPrefix(text, "send failed: "):
		m.NotifyError(text)
	case strings.HasPrefix(text, "sent to "):
		m.NotifySuccess(text)
	case text == "no drafts to resume", text == "draft not found":
		m.NotifyWarning(text)
	default:
		m.Notify(text)
	}
}

func (m *UI) UpdateFocus() {
	t := m.Theme
	m.FolderStyle = glyph.Style{FG: t.FG}
	m.ThreadStyle = glyph.Style{FG: t.FG}
	m.PreviewStyle = glyph.Style{FG: t.FG}
	m.FolderListStyle = glyph.Style{FG: t.FG}
	m.FolderSelStyle = glyph.Style{FG: t.Bright}
	m.ThreadListStyle = glyph.Style{FG: t.FG}
}

func (m *UI) UpdateThreadHeader() {
	unread := m.State.ActiveFolderUnread()
	if unread <= 0 {
		m.ThreadUnreadText = ""
		return
	}
	m.ThreadUnreadText = fmt.Sprintf("%d unread", unread)
}

func (m *UI) LoadPreview() {
	m.State.LoadConversation(m.ThreadSel, func() {
		if m.ConvView != nil {
			m.ConvView.Refresh()
		}
		m.App.RequestRender()
	})
	if m.ConvView != nil {
		m.ConvView.Layer().ScrollToTop()
		m.ConvView.Refresh()
	}
	m.App.RequestRender()
}

func (m *UI) Enter() {
	if m.State.ActiveFolderCanonical() == "Drafts" {
		if t := m.State.SelectedThread(m.ThreadSel); t != nil {
			m.Compose.ResumeDraft(t.ID)
		}
		return
	}
	if msg := m.State.SelectedMessage(m.ThreadSel); msg != nil {
		m.State.LoadPreview(*msg, m.App.Size().Width)
		m.State.MarkRead(m.ThreadSel)
		m.UpdateThreadHeader()
		m.Pane = PreviewPane
		m.UpdateFocus()
		return
	}
	m.State.ToggleThread(m.ThreadSel)
	m.State.MarkRead(m.ThreadSel)
	m.UpdateThreadHeader()
	m.LoadPreview()
}

func (m *UI) EnterFolder() {
	if m.FolderSel == m.State.CanonEnd() {
		m.ToggleFolders()
		return
	}
	m.Pane = ThreadPane
	m.UpdateFocus()
}

func (m *UI) PushUndo(undo func(), desc string) {
	if undo != nil {
		m.undoStack = append(m.undoStack, undoItem{
			run:     undo,
			message: undoMessage(desc),
		})
		m.NotifyAction(desc + " — u to undo")
		return
	}
	if desc != "" {
		m.NotifyAction(desc)
	}
}

func (m *UI) ClampThreadSel() {
	m.ThreadSel = m.State.ClampSelection(m.ThreadSel)
	m.State.SetSelected(m.ThreadSel)
}

func (m *UI) LoadFolder() {
	if m.FolderSel == m.State.CanonEnd() {
		return
	}
	actualIdx := m.FolderSel
	if m.FolderSel > m.State.CanonEnd() {
		actualIdx = m.FolderSel - 2
	}
	if actualIdx >= m.State.FolderCount() {
		return
	}
	m.State.SelectFolder(actualIdx)
	m.State.LoadThreads()
	m.State.BuildThreadDisplay()
	m.ThreadSel = 0
	m.State.SetSelected(0)
	m.UpdateThreadHeader()
	m.LoadPreview()
	m.undoStack = nil
	m.FolderTitle = m.State.FolderName(m.FolderSel)
	if m.WatchActiveFolder != nil {
		m.WatchActiveFolder()
	}
	if m.SyncActiveFolder != nil {
		m.SyncActiveFolder()
	}
}

func (m *UI) StartSearch() {
	m.SearchQuery = ""
	m.App.HideCursor()
	m.App.PushView("search")
}

func (m *UI) SubmitSearch() {
	q := m.SearchQuery
	m.SearchQuery = ""
	m.App.ShowCursor()
	m.App.PopView()
	if q == "" {
		return
	}
	results, err := m.Cache.Search(q, 50)
	if err != nil {
		m.NotifyError(fmt.Sprintf("search: %v", err))
		return
	}
	m.State.SetSearchResults(results)
	m.State.BuildThreadDisplay()
	m.ThreadSel = 0
	m.State.SetSelected(0)
	m.UpdateThreadHeader()
	m.Pane = ThreadPane
	m.UpdateFocus()
	m.Notify(fmt.Sprintf("search: %q (%d)", q, len(results)))
}

func (m *UI) CancelSearch() {
	m.SearchQuery = ""
	m.App.ShowCursor()
	m.App.PopView()
}

func (m *UI) BackspaceSearch() {
	if len(m.SearchQuery) == 0 {
		return
	}
	runes := []rune(m.SearchQuery)
	m.SearchQuery = string(runes[:len(runes)-1])
}

func (m *UI) AppendSearchKey(k riffkey.Key) bool {
	if k.Rune != 0 && k.Mod == 0 {
		m.SearchQuery += string(k.Rune)
		m.App.RequestRender()
		return true
	}
	return false
}

func (m *UI) UndoLast() {
	if len(m.undoStack) == 0 {
		m.NotifyWarning("nothing to undo")
		return
	}
	item := m.undoStack[len(m.undoStack)-1]
	item.run()
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	m.ClampThreadSel()
	m.State.BuildFolderDisplay(m.LabelsOpen)
	m.UpdateThreadHeader()
	m.LoadPreview()
	if len(m.undoStack) > 0 {
		m.NotifyAction(fmt.Sprintf("%s — %d undoable", item.message, len(m.undoStack)))
		return
	}
	m.NotifyAction(item.message)
}

func (m *UI) FoldersChanged() {
	m.State.BuildFolderDisplay(m.LabelsOpen)
	if draftsID := m.State.FolderIDByDisplayName("Drafts"); draftsID != "" {
		m.Cache.SetDraftsLabel(draftsID)
	}
	m.UpdateThreadHeader()
}

func (m *UI) ThreadsChanged() {
	m.State.LoadThreads()
	m.State.BuildFolderDisplay(m.LabelsOpen)
	m.State.BuildThreadDisplay()
	m.ThreadSel = m.State.Selected()
	m.State.SetSelected(m.ThreadSel)
	m.UpdateThreadHeader()
	m.LoadPreview()
}

func (m *UI) QueueThreadsChanged() {
	m.pendingThreadsChanged.Store(true)
}

func (m *UI) ProcessPending() {
	if m.pendingThreadsChanged.Swap(false) {
		m.ThreadsChanged()
	}
}

func (m *UI) FolderDown() {
	if m.FolderSel < m.State.FolderLen()-1 {
		m.FolderSel++
		m.LoadFolder()
	}
}

func (m *UI) FolderUp() {
	if m.FolderSel > 0 {
		m.FolderSel--
		m.LoadFolder()
	}
}

func (m *UI) ThreadDown() {
	if m.ThreadSel < m.State.ThreadLen()-1 {
		m.ThreadSel++
		m.State.SetSelected(m.ThreadSel)
		m.LoadPreview()
	}
}

func (m *UI) ThreadUp() {
	if m.ThreadSel > 0 {
		m.ThreadSel--
		m.State.SetSelected(m.ThreadSel)
		m.LoadPreview()
	}
}

func (m *UI) ThreadTop() {
	m.ThreadSel = 0
	m.State.SetSelected(m.ThreadSel)
	m.LoadPreview()
}

func (m *UI) ThreadBottom() {
	m.ThreadSel = len(*m.State.ThreadRows()) - 1
	m.State.SetSelected(m.ThreadSel)
	m.LoadPreview()
}

func (m *UI) PreviewDown() {
	if m.ConvView != nil {
		m.ConvView.Layer().ScrollDown(1)
		m.UpdatePreviewScroll()
	}
}

func (m *UI) PreviewUp() {
	if m.ConvView != nil {
		m.ConvView.Layer().ScrollUp(1)
		m.UpdatePreviewScroll()
	}
}

func (m *UI) PreviewHalfPageUp() {
	if m.ConvView == nil {
		return
	}
	m.ConvView.Layer().HalfPageUp()
	m.UpdatePreviewScroll()
}

func (m *UI) PreviewHalfPageDown() {
	if m.ConvView == nil {
		return
	}
	m.ConvView.Layer().HalfPageDown()
	m.UpdatePreviewScroll()
}

func (m *UI) PreviewTop() {
	if m.ConvView == nil {
		return
	}
	m.ConvView.Layer().ScrollToTop()
	m.UpdatePreviewScroll()
}

func (m *UI) PreviewBottom() {
	if m.ConvView == nil {
		return
	}
	m.ConvView.Layer().ScrollToEnd()
	m.UpdatePreviewScroll()
}

func (m *UI) FocusRight() {
	if m.Pane < PreviewPane {
		m.Pane++
		m.UpdateFocus()
	}
}

func (m *UI) FocusFolders() {
	m.Pane = FolderPane
	m.UpdateFocus()
}

func (m *UI) FocusThreads() {
	m.Pane = ThreadPane
	m.UpdateFocus()
}

func (m *UI) FocusPreview() {
	m.Pane = PreviewPane
	m.UpdateFocus()
}

func (m *UI) FocusLeft() {
	if m.Pane > FolderPane {
		m.Pane--
		m.UpdateFocus()
	}
}

func (m *UI) FocusNext() {
	m.Pane = (m.Pane + 1) % 3
	m.UpdateFocus()
}

func (m *UI) FocusPrev() {
	m.Pane = (m.Pane + 2) % 3
	m.UpdateFocus()
}

func (m *UI) Escape() {
	if m.HelpOpen {
		m.HelpOpen = false
		return
	}
	m.FocusLeft()
}

func (m *UI) ToggleThread() {
	m.State.ToggleThread(m.ThreadSel)
}

func (m *UI) ThreadAction(label string, fn func()) {
	if m.State.ThreadLen() == 0 {
		m.NotifyWarning(label + ": no thread selected")
		return
	}
	fn()
}

func (m *UI) ComposeNew() {
	m.Compose.Open()
}

func (m *UI) ResumeDraft() {
	m.Compose.ResumeLast()
}

func (m *UI) ReplySelected() {
	if t := m.State.SelectedThread(m.ThreadSel); t != nil {
		if row := m.State.ThreadRowAt(m.ThreadSel); row != nil && row.MsgIdx < 0 {
			if m.State.ActiveFolderCanonical() == "Drafts" {
				m.Compose.ResumeDraft(t.ID)
				return
			}
			if m.Compose.OpenInlineReply != nil {
				m.Compose.OpenInlineReply(*t)
				return
			}
			m.Compose.Open()
			m.Compose.SetupReply(*t)
		}
	}
}

func (m *UI) ReplyAllSelected() {
	if t := m.State.SelectedThread(m.ThreadSel); t != nil {
		if row := m.State.ThreadRowAt(m.ThreadSel); row != nil && row.MsgIdx < 0 {
			if m.State.ActiveFolderCanonical() == "Drafts" {
				m.Compose.ResumeDraft(t.ID)
				return
			}
			m.Compose.Open()
			if m.Compose.SetupReplyAll != nil {
				m.Compose.SetupReplyAll(*t)
				return
			}
			m.Compose.SetupReply(*t)
		}
	}
}

func (m *UI) ForwardSelected() {
	if t := m.State.SelectedThread(m.ThreadSel); t != nil {
		if row := m.State.ThreadRowAt(m.ThreadSel); row != nil && row.MsgIdx < 0 {
			m.Compose.Open()
			if m.Compose.SetupForward != nil {
				m.Compose.SetupForward(*t)
			}
		}
	}
}

func (m *UI) Archive() {
	sel := m.ThreadSel
	m.PushUndo(m.State.Archive(m.ThreadSel))
	m.afterThreadAction(sel)
}

func (m *UI) ArchiveSelected() {
	m.ThreadAction("archive", m.Archive)
}

func (m *UI) Delete() {
	sel := m.ThreadSel
	m.PushUndo(m.State.Delete(m.ThreadSel))
	m.afterThreadAction(sel)
}

func (m *UI) Spam() {
	sel := m.ThreadSel
	m.PushUndo(m.State.Spam(m.ThreadSel))
	m.afterThreadAction(sel)
}

func (m *UI) MoveSelectedToFolder(folderID, folderName string) {
	m.ThreadAction("move", func() {
		sel := m.ThreadSel
		m.PushUndo(m.State.Move(m.ThreadSel, folderID, folderName))
		m.afterThreadAction(sel)
	})
}

func (m *UI) MoveTargetFolders() []provider.Folder {
	return m.State.MoveTargetFolders()
}

func (m *UI) DeleteSelected() {
	m.ThreadAction("delete", m.Delete)
}

func (m *UI) ToggleStar() {
	m.PushUndo(m.State.ToggleStar(m.ThreadSel))
}

func (m *UI) ToggleStarSelected() {
	m.ThreadAction("star", m.ToggleStar)
}

func (m *UI) ToggleRead() {
	m.PushUndo(m.State.ToggleRead(m.ThreadSel))
	m.State.BuildFolderDisplay(m.LabelsOpen)
	m.UpdateThreadHeader()
}

func (m *UI) ToggleReadSelected() {
	m.ThreadAction("read", m.ToggleRead)
}

func (m *UI) ToggleFolders() {
	m.LabelsOpen = !m.LabelsOpen
	m.State.BuildFolderDisplay(m.LabelsOpen)
	if m.FolderSel >= m.State.FolderLen() {
		m.FolderSel = m.State.FolderLen() - 1
	}
	if m.FolderSel < 0 {
		m.FolderSel = 0
	}
	m.UpdateThreadHeader()
	m.Notify("folders toggled")
}

func (m *UI) RefreshMail() {
	m.Notify("syncing...")
	if m.SyncActiveFolder != nil {
		m.SyncActiveFolder()
	}
}

func (m *UI) LoadMoreThreads() {
	limit := m.State.LoadMoreThreads()
	if m.ThreadSel >= m.State.ThreadLen() {
		m.ThreadSel = m.State.ThreadLen() - 1
	}
	if m.ThreadSel < 0 {
		m.ThreadSel = 0
	}
	m.State.SetSelected(m.ThreadSel)
	m.LoadPreview()
	m.Notify(fmt.Sprintf("showing %d cached threads", limit))
}

func (m *UI) CopySenderAddress() {
	msg := m.State.SelectedMessage(m.ThreadSel)
	if msg == nil {
		msg = m.State.LastMessage(m.ThreadSel)
	}
	if msg == nil || strings.TrimSpace(msg.From.Email) == "" {
		m.NotifyWarning("copy sender: no sender")
		return
	}
	if err := copyText(msg.From.Email); err != nil {
		m.NotifyError(fmt.Sprintf("copy sender: %v", err))
		return
	}
	m.Notify(fmt.Sprintf("copied %s", msg.From.Email))
}

func (m *UI) ShowKeyboardHelp() {
	if m.HelpOpen {
		return
	}
	m.HelpOpen = true
}

func (m *UI) ToggleKeyboardHelp() {
	if m.HelpOpen {
		m.HelpOpen = false
		return
	}
	m.HelpOpen = true
}

func (m *UI) TickFrame() {
	m.Frame++
	m.UpdateStatusOverlay()
	m.App.RequestRender()
}

func (m *UI) afterThreadAction(sel int) {
	m.ThreadSel = m.State.ClampSelection(sel)
	m.ClampThreadSel()
	m.State.BuildFolderDisplay(m.LabelsOpen)
	m.UpdateThreadHeader()
	m.LoadPreview()
}

func undoMessage(desc string) string {
	switch {
	case strings.HasPrefix(desc, "deleted "):
		return "restored " + strings.TrimPrefix(desc, "deleted ")
	case strings.HasPrefix(desc, "archived "):
		return "restored " + strings.TrimPrefix(desc, "archived ")
	default:
		return "undid " + desc
	}
}
