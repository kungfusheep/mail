package mailcommands

type Command struct {
	Label       string
	Description string
	Key         string
	Section     string
	Action      func()
}

type Actions struct {
	ComposeNew       func()
	ResumeDraft      func()
	ReplySelected    func()
	RefreshMail      func()
	ToggleFolders    func()
	FocusFolders     func()
	FocusThreads     func()
	FocusPreview     func()
	SearchMail       func()
	OpenSelected     func()
	ArchiveSelected  func()
	DeleteSelected   func()
	ToggleStar       func()
	ToggleRead       func()
	UndoLast         func()
	ShowKeyboardHelp func()
	Quit             func()
}

func Build(actions Actions) []Command {
	return []Command{
		{Label: "Compose New", Description: "start a fresh message", Key: "c", Section: "compose", Action: actions.ComposeNew},
		{Label: "Resume Draft", Description: "continue the latest saved draft", Key: "C", Section: "compose", Action: actions.ResumeDraft},
		{Label: "Reply To Selected Thread", Description: "reply to the current conversation", Key: "r", Section: "compose", Action: actions.ReplySelected},
		{Label: "Refresh Mail", Description: "process pending changes and sync this folder", Key: "sync", Section: "mail", Action: actions.RefreshMail},
		{Label: "Toggle Folders", Description: "show or hide grouped labels", Key: "enter", Section: "navigation", Action: actions.ToggleFolders},
		{Label: "Focus Folders", Description: "move focus to the folder pane", Key: "h", Section: "navigation", Action: actions.FocusFolders},
		{Label: "Focus Threads", Description: "move focus to the thread list", Key: "tab", Section: "navigation", Action: actions.FocusThreads},
		{Label: "Focus Preview", Description: "move focus to the message preview", Key: "l", Section: "navigation", Action: actions.FocusPreview},
		{Label: "Search Mail", Description: "search cached messages", Key: "/", Section: "mail", Action: actions.SearchMail},
		{Label: "Open Selected Thread", Description: "open, expand, or preview the selected row", Key: "enter", Section: "thread", Action: actions.OpenSelected},
		{Label: "Archive Selected Thread", Description: "move the selected thread out of inbox", Key: "a", Section: "thread", Action: actions.ArchiveSelected},
		{Label: "Delete Selected Thread", Description: "move the selected thread to trash", Key: "d", Section: "thread", Action: actions.DeleteSelected},
		{Label: "Toggle Star", Description: "star or unstar the selected thread", Key: "s", Section: "thread", Action: actions.ToggleStar},
		{Label: "Toggle Read", Description: "mark selected thread read or unread", Key: "e", Section: "thread", Action: actions.ToggleRead},
		{Label: "Undo Last Thread Action", Description: "restore the latest archive/delete/read/star change", Key: "u", Section: "thread", Action: actions.UndoLast},
		{Label: "Show Keyboard Help", Description: "open the in-app keybinding help", Key: "?", Section: "help", Action: actions.ShowKeyboardHelp},
		{Label: "Quit Mail", Description: "exit the app", Key: "q", Section: "system", Action: actions.Quit},
	}
}
