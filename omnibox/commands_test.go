package omnibox

import "testing"

func TestBuildKeepsOmniboxCatalogueOrder(t *testing.T) {
	commands := buildCommands(commandActions{})

	want := []command{
		{Label: "Compose New", Key: "c", Section: "compose"},
		{Label: "Resume Draft", Key: "C", Section: "compose"},
		{Label: "Reply To Selected Thread", Key: "r", Section: "compose"},
		{Label: "Refresh Mail", Key: "sync", Section: "mail"},
		{Label: "Toggle Folders", Key: "enter", Section: "navigation"},
		{Label: "Focus Folders", Key: "h", Section: "navigation"},
		{Label: "Focus Threads", Key: "tab", Section: "navigation"},
		{Label: "Focus Preview", Key: "l", Section: "navigation"},
		{Label: "Search Mail", Key: "/", Section: "mail"},
		{Label: "Open Selected Thread", Key: "enter", Section: "thread"},
		{Label: "Archive Selected Thread", Key: "a", Section: "thread"},
		{Label: "Delete Selected Thread", Key: "d", Section: "thread"},
		{Label: "Toggle Star", Key: "s", Section: "thread"},
		{Label: "Toggle Read", Key: "e", Section: "thread"},
		{Label: "Undo Last Thread Action", Key: "u", Section: "thread"},
		{Label: "Show Keyboard Help", Key: "?", Section: "help"},
		{Label: "Quit Mail", Key: "q", Section: "system"},
	}

	if len(commands) != len(want) {
		t.Fatalf("got %d commands, want %d", len(commands), len(want))
	}

	for i := range want {
		if commands[i].Label != want[i].Label {
			t.Fatalf("command %d label = %q, want %q", i, commands[i].Label, want[i].Label)
		}
		if commands[i].Key != want[i].Key {
			t.Fatalf("command %q key = %q, want %q", commands[i].Label, commands[i].Key, want[i].Key)
		}
		if commands[i].Section != want[i].Section {
			t.Fatalf("command %q section = %q, want %q", commands[i].Label, commands[i].Section, want[i].Section)
		}
		if commands[i].Description == "" {
			t.Fatalf("command %q has no description", commands[i].Label)
		}
	}
}

func TestBuildWiresActions(t *testing.T) {
	called := false
	commands := buildCommands(commandActions{
		ComposeNew: func() {
			called = true
		},
	})

	if commands[0].Action == nil {
		t.Fatal("compose command action is nil")
	}
	commands[0].Action()
	if !called {
		t.Fatal("compose command did not call its action")
	}
}
