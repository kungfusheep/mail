package omnibox

import "strings"

type command struct {
	Label       string
	Description string
	Key         string
	Section     string
	Action      func()
	Preview     func()
}

type Item struct {
	Label       string
	Description string
	Key         string
	Section     string
	Action      func()
	Preview     func()
}

func commandFromItem(item Item) command {
	return command{
		Label:       item.Label,
		Description: item.Description,
		Key:         item.Key,
		Section:     item.Section,
		Action:      item.Action,
		Preview:     item.Preview,
	}
}

type moveTarget struct {
	ID   string
	Name string
}

type commandActions struct {
	ComposeNew       func()
	ResumeDraft      func()
	ReplySelected    func()
	ReplyAllSelected func()
	ForwardSelected  func()
	RefreshMail      func()
	ToggleFolders    func()
	FocusFolders     func()
	FocusThreads     func()
	FocusPreview     func()
	SearchMail       func()
	LoadMoreThreads  func()
	OpenSelected     func()
	ArchiveSelected  func()
	DeleteSelected   func()
	SpamSelected     func()
	SnoozeSelected   func()
	MoveTargets      []moveTarget
	MoveSelectedTo   func(folderID, folderName string)
	ToggleStar       func()
	ToggleRead       func()
	CopySender       func()
	UndoLast         func()
	ShowKeyboardHelp func()
	SwitchTheme      func()
	Quit             func()
}

func buildCommands(actions commandActions) []command {
	commands := []command{
		{Label: "Compose New", Description: "start a fresh message", Key: "c", Section: "compose", Action: actions.ComposeNew},
		{Label: "Resume Draft", Description: "continue the latest saved draft", Key: "C", Section: "compose", Action: actions.ResumeDraft},
		{Label: "Reply To Selected Thread", Description: "reply to the current conversation", Key: "r", Section: "compose", Action: actions.ReplySelected},
		{Label: "Reply All To Selected Thread", Description: "reply to every recipient in the current conversation", Key: "ra", Section: "compose", Action: actions.ReplyAllSelected},
		{Label: "Forward Selected Thread", Description: "forward the current conversation", Key: "fwd", Section: "compose", Action: actions.ForwardSelected},
		{Label: "Refresh Mail", Description: "process pending changes and sync this folder", Key: "sync", Section: "mail", Action: actions.RefreshMail},
		{Label: "Toggle Folders", Description: "show or hide grouped labels", Key: "enter", Section: "navigation", Action: actions.ToggleFolders},
		{Label: "Focus Folders", Description: "move focus to the folder pane", Key: "h", Section: "navigation", Action: actions.FocusFolders},
		{Label: "Focus Threads", Description: "move focus to the thread list", Key: "tab", Section: "navigation", Action: actions.FocusThreads},
		{Label: "Focus Preview", Description: "move focus to the message preview", Key: "l", Section: "navigation", Action: actions.FocusPreview},
		{Label: "Search Mail", Description: "search cached messages", Key: "/", Section: "mail", Action: actions.SearchMail},
		{Label: "Load More Threads", Description: "show more cached mail in this folder", Key: "more", Section: "mail", Action: actions.LoadMoreThreads},
		{Label: "Open Selected Thread", Description: "open, expand, or preview the selected row", Key: "enter", Section: "thread", Action: actions.OpenSelected},
		{Label: "Archive Selected Thread", Description: "move the selected thread out of inbox", Key: "a", Section: "thread", Action: actions.ArchiveSelected},
		{Label: "Delete Selected Thread", Description: "move the selected thread to trash", Key: "d", Section: "thread", Action: actions.DeleteSelected},
		{Label: "Move Selected Thread to Spam", Description: "move the selected thread to spam", Key: "spam", Section: "thread", Action: actions.SpamSelected},
		{Label: "Snooze Selected Thread Until Tomorrow", Description: "move the selected thread to Snoozed until tomorrow morning", Key: "z", Section: "thread", Action: actions.SnoozeSelected},
	}
	for _, target := range actions.MoveTargets {
		target := target
		commands = append(commands, command{
			Label:       "Move Selected Thread to " + target.Name,
			Description: "move the selected thread to " + target.Name,
			Key:         "move " + strings.ToLower(target.Name),
			Section:     "thread",
			Action: func() {
				if actions.MoveSelectedTo != nil {
					actions.MoveSelectedTo(target.ID, target.Name)
				}
			},
		})
	}
	commands = append(commands,
		command{Label: "Toggle Star", Description: "star or unstar the selected thread", Key: "s", Section: "thread", Action: actions.ToggleStar},
		command{Label: "Toggle Read", Description: "mark selected thread read or unread", Key: "e", Section: "thread", Action: actions.ToggleRead},
		command{Label: "Copy Sender Address", Description: "copy the selected message sender email", Key: "copy sender", Section: "message", Action: actions.CopySender},
		command{Label: "Undo Last Thread Action", Description: "restore the latest archive/delete/read/star change", Key: "u", Section: "thread", Action: actions.UndoLast},
		command{Label: "Show Keyboard Help", Description: "open the in-app keybinding help", Key: "?", Section: "help", Action: actions.ShowKeyboardHelp},
		command{Label: "Switch Theme", Description: "choose light or dark palette", Key: "theme", Section: "system", Action: actions.SwitchTheme},
		command{Label: "Quit Mail", Description: "exit the app", Key: "q", Section: "system", Action: actions.Quit},
	)
	return commands
}
