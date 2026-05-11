package helpdialog

import (
	"testing"

	"github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/theme"
)

func TestMailboxHelpRowsArePresent(t *testing.T) {
	if len(mailboxNavigationRows) == 0 {
		t.Fatal("expected navigation help rows")
	}
	if len(mailboxActionRows) == 0 {
		t.Fatal("expected action help rows")
	}

	for _, rows := range [][]row{mailboxNavigationRows, mailboxActionRows} {
		for _, r := range rows {
			if r.key == "" || r.desc == "" {
				t.Fatalf("expected complete help row, got key=%q desc=%q", r.key, r.desc)
			}
		}
	}
}

func TestMailboxBuildsComponent(t *testing.T) {
	open := true
	var ref glyph.NodeRef

	if Mailbox(&open, &ref, theme.Dark()) == nil {
		t.Fatal("expected mailbox help component")
	}
}
