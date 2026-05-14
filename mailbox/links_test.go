package mailbox

import "testing"

func TestOpenSystemURLRejectsUnsafeSchemes(t *testing.T) {
	if err := OpenSystemURL("javascript:alert(1)"); err == nil {
		t.Fatal("OpenSystemURL accepted javascript URL")
	}
	if err := OpenSystemURL("/tmp/file"); err == nil {
		t.Fatal("OpenSystemURL accepted URL without scheme")
	}
}

func TestStateOpenLinkUsesConfiguredOpener(t *testing.T) {
	mb := NewState(nil, "test@example.com")
	var opened string
	mb.SetLinkOpener(func(href string) error {
		opened = href
		return nil
	})

	mb.OpenLink("https://example.test")

	if opened != "https://example.test" {
		t.Fatalf("opened = %q, want configured link", opened)
	}
}
