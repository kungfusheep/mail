package preview

import "testing"

func TestStripQuoted_OnWrote(t *testing.T) {
	body := `Thanks, sounds good!

On 13 Apr 2026, at 12:35, someone@test.com wrote:

> hey, could you reply to this?
> thanks`

	got := StripQuoted(body)
	want := "Thanks, sounds good!"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripQuoted_QuotePrefixed(t *testing.T) {
	body := `My reply here.

> original message line 1
> original message line 2`

	got := StripQuoted(body)
	want := "My reply here."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripQuoted_MobileSig(t *testing.T) {
	body := `Quick reply

Sent from my iPhone`

	got := StripQuoted(body)
	want := "Quick reply"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripQuoted_SignatureMarker(t *testing.T) {
	body := `Main content here.

--
John Smith
Engineering Lead`

	got := StripQuoted(body)
	want := "Main content here."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripQuoted_NoQuotes(t *testing.T) {
	body := `Just a plain message with no quotes.

Multiple paragraphs are fine.`

	got := StripQuoted(body)
	if got != body {
		t.Errorf("should return body unchanged, got %q", got)
	}
}

func TestStripQuoted_ForwardedMessage(t *testing.T) {
	body := `FYI see below.

---------- Forwarded message ----------
From: someone
To: someone else
Subject: thing`

	got := StripQuoted(body)
	want := "FYI see below."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripQuoted_ComplexReply(t *testing.T) {
	body := `Reply Sent from my iPhone
On 13 Apr 2026, at 12:35, kungfusheep@gmail.com wrote:

> test email hello - could you reply to this please? xx`

	got := StripQuoted(body)
	// should keep "Reply" but strip the iPhone sig line before "On ... wrote:"
	// actually "Sent from my iPhone" is embedded in the first line here,
	// so the whole first line survives. The "On ... wrote:" breaks after.
	want := "Reply Sent from my iPhone"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
