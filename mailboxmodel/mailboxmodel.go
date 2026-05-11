package mailboxmodel

import (
	"fmt"
	"strings"
	"time"

	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/composeview"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/mailruntime"
	"github.com/kungfusheep/mail/theme"
	"github.com/kungfusheep/mail/ui"
	"github.com/kungfusheep/riffkey"
)

const (
	FolderPane = iota
	ThreadPane
	PreviewPane
)

type Config struct {
	App     *glyph.App
	Cache   *cache.Cache
	Mailbox *mailbox.Mailbox
	Theme   theme.Theme
}

type undoItem struct {
	run     func()
	message string
}

type MailboxModel struct {
	App     *glyph.App
	Cache   *cache.Cache
	Mailbox *mailbox.Mailbox
	Theme   theme.Theme
	Compose composeview.Controls
	Runtime *mailruntime.Runtime

	FolderSel        int
	ThreadSel        int
	LabelsOpen       bool
	HelpOpen         bool
	HelpRef          glyph.NodeRef
	Frame            int
	FolderTitle      string
	SearchQuery      string
	ThreadUnreadText string
	StatusVisible    bool
	Pane             int

	FolderStyle     glyph.Style
	ThreadStyle     glyph.Style
	PreviewStyle    glyph.Style
	FolderListStyle glyph.Style
	FolderSelStyle  glyph.Style
	ThreadListStyle glyph.Style

	ConvView *glyph.ScrollViewC

	statusFeed *ui.Feed
	undoStack  []undoItem
}

func New(cfg Config) *MailboxModel {
	m := &MailboxModel{
		App:             cfg.App,
		Cache:           cfg.Cache,
		Mailbox:         cfg.Mailbox,
		Theme:           cfg.Theme,
		FolderTitle:     "Inbox",
		Pane:            ThreadPane,
		FolderStyle:     glyph.Style{FG: cfg.Theme.Dim},
		ThreadStyle:     glyph.Style{FG: cfg.Theme.FG},
		PreviewStyle:    glyph.Style{FG: cfg.Theme.Dim},
		FolderListStyle: glyph.Style{FG: cfg.Theme.Dim},
		FolderSelStyle:  glyph.Style{FG: cfg.Theme.Dim},
		ThreadListStyle: glyph.Style{FG: cfg.Theme.FG},
		statusFeed:      ui.NewFeed(time.Now),
	}
	m.UpdateThreadHeader()
	m.UpdateStatusOverlay()
	return m
}

func (m *MailboxModel) SetCompose(comp composeview.Controls) {
	m.Compose = comp
}

func (m *MailboxModel) SetRuntime(rt *mailruntime.Runtime) {
	m.Runtime = rt
}

func (m *MailboxModel) SetConversationView(sv *glyph.ScrollViewC) {
	m.ConvView = sv
}

func (m *MailboxModel) StatusItems() *[]ui.Notification {
	return m.statusFeed.Items()
}

func (m *MailboxModel) UpdateStatusOverlay() {
	m.statusFeed.Update()
	m.StatusVisible = len(*m.statusFeed.Items()) > 0
}

func (m *MailboxModel) Notify(text string) {
	m.statusFeed.Push(text)
	m.UpdateStatusOverlay()
	m.App.RequestRender()
}

func (m *MailboxModel) UpdateFocus() {
	t := m.Theme
	m.FolderStyle = glyph.Style{FG: t.Dim}
	m.ThreadStyle = glyph.Style{FG: t.Dim}
	m.FolderListStyle = glyph.Style{FG: t.Dim}
	m.FolderSelStyle = glyph.Style{FG: t.Dim}
	m.ThreadListStyle = glyph.Style{FG: t.Dim}
	m.PreviewStyle = glyph.Style{FG: t.Dim}
	switch m.Pane {
	case FolderPane:
		m.FolderStyle = glyph.Style{FG: t.FG}
		m.FolderListStyle = glyph.Style{FG: t.FG}
		m.FolderSelStyle = glyph.Style{FG: t.Bright}
	case ThreadPane:
		m.ThreadStyle = glyph.Style{FG: t.FG}
		m.ThreadListStyle = glyph.Style{FG: t.FG}
	case PreviewPane:
		m.PreviewStyle = glyph.Style{FG: t.FG}
	}
}

func (m *MailboxModel) UpdateThreadHeader() {
	unread := m.Mailbox.ActiveFolderUnread()
	if unread <= 0 {
		m.ThreadUnreadText = ""
		return
	}
	m.ThreadUnreadText = fmt.Sprintf("%d unread", unread)
}

func (m *MailboxModel) LoadPreview() {
	m.Mailbox.LoadConversation(m.ThreadSel, func() {
		if m.ConvView != nil {
			m.ConvView.Refresh()
		}
		m.App.RequestRender()
	})
	if m.ConvView != nil {
		m.ConvView.Refresh()
	}
	m.App.RequestRender()
}

func (m *MailboxModel) Enter() {
	if m.Mailbox.ActiveFolderCanonical() == "Drafts" {
		if t := m.Mailbox.SelectedThread(m.ThreadSel); t != nil {
			m.Compose.ResumeDraft(t.ID)
		}
		return
	}
	if msg := m.Mailbox.SelectedMessage(m.ThreadSel); msg != nil {
		m.Mailbox.LoadPreview(*msg, m.App.Size().Width)
		m.Mailbox.MarkRead(m.ThreadSel)
		m.UpdateThreadHeader()
		m.Pane = PreviewPane
		m.UpdateFocus()
		return
	}
	m.Mailbox.ToggleThread(m.ThreadSel)
	m.Mailbox.MarkRead(m.ThreadSel)
	m.UpdateThreadHeader()
	m.LoadPreview()
}

func (m *MailboxModel) EnterFolder() {
	if m.FolderSel == m.Mailbox.CanonEnd() {
		m.ToggleFolders()
		return
	}
	m.Pane = ThreadPane
	m.UpdateFocus()
}

func (m *MailboxModel) PushUndo(undo func(), desc string) {
	if undo != nil {
		m.undoStack = append(m.undoStack, undoItem{
			run:     undo,
			message: undoMessage(desc),
		})
		m.Notify(desc + " — u to undo")
		return
	}
	if desc != "" {
		m.Notify(desc)
	}
}

func (m *MailboxModel) ClampThreadSel() {
	if m.ThreadSel >= m.Mailbox.ThreadLen() {
		m.ThreadSel = m.Mailbox.ThreadLen() - 1
	}
	if m.ThreadSel < 0 {
		m.ThreadSel = 0
	}
	m.Mailbox.SetSelected(m.ThreadSel)
}

func (m *MailboxModel) LoadFolder() {
	if m.FolderSel == m.Mailbox.CanonEnd() {
		return
	}
	actualIdx := m.FolderSel
	if m.FolderSel > m.Mailbox.CanonEnd() {
		actualIdx = m.FolderSel - 2
	}
	if actualIdx >= m.Mailbox.FolderCount() {
		return
	}
	m.Mailbox.SelectFolder(actualIdx)
	m.Mailbox.LoadThreads()
	m.Mailbox.BuildThreadDisplay()
	m.ThreadSel = 0
	m.Mailbox.SetSelected(0)
	m.UpdateThreadHeader()
	m.LoadPreview()
	m.undoStack = nil
	m.FolderTitle = m.Mailbox.FolderName(m.FolderSel)
	if m.Runtime != nil {
		m.Runtime.WatchActiveFolder()
		m.Runtime.SyncActiveFolder()
	}
}

func (m *MailboxModel) StartSearch() {
	m.SearchQuery = ""
	m.App.HideCursor()
	m.App.PushView("search")
}

func (m *MailboxModel) SubmitSearch() {
	q := m.SearchQuery
	m.SearchQuery = ""
	m.App.ShowCursor()
	m.App.PopView()
	if q == "" {
		return
	}
	results, err := m.Cache.Search(q, 50)
	if err != nil {
		m.Notify(fmt.Sprintf("search: %v", err))
		return
	}
	m.Mailbox.SetSearchResults(results)
	m.Mailbox.BuildThreadDisplay()
	m.ThreadSel = 0
	m.Mailbox.SetSelected(0)
	m.UpdateThreadHeader()
	m.Pane = ThreadPane
	m.UpdateFocus()
	m.Notify(fmt.Sprintf("search: %q (%d)", q, len(results)))
}

func (m *MailboxModel) CancelSearch() {
	m.SearchQuery = ""
	m.App.ShowCursor()
	m.App.PopView()
}

func (m *MailboxModel) BackspaceSearch() {
	if len(m.SearchQuery) == 0 {
		return
	}
	runes := []rune(m.SearchQuery)
	m.SearchQuery = string(runes[:len(runes)-1])
}

func (m *MailboxModel) AppendSearchKey(k riffkey.Key) bool {
	if k.Rune != 0 && k.Mod == 0 {
		m.SearchQuery += string(k.Rune)
		m.App.RequestRender()
		return true
	}
	return false
}

func (m *MailboxModel) UndoLast() {
	if len(m.undoStack) == 0 {
		m.Notify("nothing to undo")
		return
	}
	item := m.undoStack[len(m.undoStack)-1]
	item.run()
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	m.ClampThreadSel()
	m.Mailbox.BuildFolderDisplay(m.LabelsOpen)
	m.UpdateThreadHeader()
	m.LoadPreview()
	if len(m.undoStack) > 0 {
		m.Notify(fmt.Sprintf("%s — %d undoable", item.message, len(m.undoStack)))
		return
	}
	m.Notify(item.message)
}

func (m *MailboxModel) FoldersChanged() {
	m.Mailbox.BuildFolderDisplay(m.LabelsOpen)
	if draftsID := m.Mailbox.FolderIDByDisplayName("Drafts"); draftsID != "" {
		m.Cache.SetDraftsLabel(draftsID)
	}
	m.UpdateThreadHeader()
}

func (m *MailboxModel) ThreadsChanged() {
	m.Mailbox.BuildFolderDisplay(m.LabelsOpen)
	m.Mailbox.BuildThreadDisplay()
	m.ClampThreadSel()
	m.UpdateThreadHeader()
	m.LoadPreview()
}

func (m *MailboxModel) FolderDown() {
	if m.FolderSel < m.Mailbox.FolderLen()-1 {
		m.FolderSel++
		m.LoadFolder()
	}
}

func (m *MailboxModel) FolderUp() {
	if m.FolderSel > 0 {
		m.FolderSel--
		m.LoadFolder()
	}
}

func (m *MailboxModel) ThreadDown() {
	if m.ThreadSel < m.Mailbox.ThreadLen()-1 {
		m.ThreadSel++
		m.Mailbox.SetSelected(m.ThreadSel)
		m.LoadPreview()
	}
}

func (m *MailboxModel) ThreadUp() {
	if m.ThreadSel > 0 {
		m.ThreadSel--
		m.Mailbox.SetSelected(m.ThreadSel)
		m.LoadPreview()
	}
}

func (m *MailboxModel) PreviewDown() {
	if m.ConvView != nil {
		m.ConvView.Layer().ScrollDown(1)
	}
}

func (m *MailboxModel) PreviewUp() {
	if m.ConvView != nil {
		m.ConvView.Layer().ScrollUp(1)
	}
}

func (m *MailboxModel) FocusRight() {
	if m.Pane < PreviewPane {
		m.Pane++
		m.UpdateFocus()
	}
}

func (m *MailboxModel) FocusFolders() {
	m.Pane = FolderPane
	m.UpdateFocus()
}

func (m *MailboxModel) FocusThreads() {
	m.Pane = ThreadPane
	m.UpdateFocus()
}

func (m *MailboxModel) FocusPreview() {
	m.Pane = PreviewPane
	m.UpdateFocus()
}

func (m *MailboxModel) FocusLeft() {
	if m.Pane > FolderPane {
		m.Pane--
		m.UpdateFocus()
	}
}

func (m *MailboxModel) FocusNext() {
	m.Pane = (m.Pane + 1) % 3
	m.UpdateFocus()
}

func (m *MailboxModel) FocusPrev() {
	m.Pane = (m.Pane + 2) % 3
	m.UpdateFocus()
}

func (m *MailboxModel) Escape() {
	if m.HelpOpen {
		m.HelpOpen = false
		return
	}
	m.FocusLeft()
}

func (m *MailboxModel) ToggleThread() {
	m.Mailbox.ToggleThread(m.ThreadSel)
}

func (m *MailboxModel) ThreadAction(label string, fn func()) {
	if m.Mailbox.ThreadLen() == 0 {
		m.Notify(label + ": no thread selected")
		return
	}
	fn()
}

func (m *MailboxModel) ComposeNew() {
	m.Compose.Open()
}

func (m *MailboxModel) ResumeDraft() {
	m.Compose.ResumeLast()
}

func (m *MailboxModel) ReplySelected() {
	if t := m.Mailbox.SelectedThread(m.ThreadSel); t != nil {
		if row := m.Mailbox.ThreadRowAt(m.ThreadSel); row != nil && row.MsgIdx < 0 {
			if m.Mailbox.ActiveFolderCanonical() == "Drafts" {
				m.Compose.ResumeDraft(t.ID)
				return
			}
			m.Compose.Open()
			m.Compose.SetupReply(*t)
		}
	}
}

func (m *MailboxModel) Archive() {
	m.PushUndo(m.Mailbox.Archive(m.ThreadSel))
	m.afterThreadAction()
}

func (m *MailboxModel) ArchiveSelected() {
	m.ThreadAction("archive", m.Archive)
}

func (m *MailboxModel) Delete() {
	m.PushUndo(m.Mailbox.Delete(m.ThreadSel))
	m.afterThreadAction()
}

func (m *MailboxModel) DeleteSelected() {
	m.ThreadAction("delete", m.Delete)
}

func (m *MailboxModel) ToggleStar() {
	m.PushUndo(m.Mailbox.ToggleStar(m.ThreadSel))
}

func (m *MailboxModel) ToggleStarSelected() {
	m.ThreadAction("star", m.ToggleStar)
}

func (m *MailboxModel) ToggleRead() {
	m.PushUndo(m.Mailbox.ToggleRead(m.ThreadSel))
	m.Mailbox.BuildFolderDisplay(m.LabelsOpen)
	m.UpdateThreadHeader()
}

func (m *MailboxModel) ToggleReadSelected() {
	m.ThreadAction("read", m.ToggleRead)
}

func (m *MailboxModel) ToggleFolders() {
	m.LabelsOpen = !m.LabelsOpen
	m.Mailbox.BuildFolderDisplay(m.LabelsOpen)
	if m.FolderSel >= m.Mailbox.FolderLen() {
		m.FolderSel = m.Mailbox.FolderLen() - 1
	}
	if m.FolderSel < 0 {
		m.FolderSel = 0
	}
	m.UpdateThreadHeader()
	m.Notify("folders toggled")
}

func (m *MailboxModel) RefreshMail() {
	m.Notify("syncing...")
	if m.Runtime != nil {
		m.Runtime.SyncActiveFolder()
	}
}

func (m *MailboxModel) ShowKeyboardHelp() {
	m.HelpOpen = true
}

func (m *MailboxModel) ToggleKeyboardHelp() {
	m.HelpOpen = !m.HelpOpen
}

func (m *MailboxModel) TickFrame() {
	m.Frame++
	m.UpdateStatusOverlay()
	m.App.RequestRender()
}

func (m *MailboxModel) afterThreadAction() {
	m.ClampThreadSel()
	m.Mailbox.BuildFolderDisplay(m.LabelsOpen)
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
