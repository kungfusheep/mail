package preview

import "testing"

func TestSanitize_StripsZeroWidth(t *testing.T) {
	input := "hello\u034F world\u200B!"
	got := Sanitize(input)
	want := "hello world!"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSanitize_PreservesASCII(t *testing.T) {
	input := "plain ascii text\nwith newlines\tand tabs"
	got := Sanitize(input)
	if got != input {
		t.Errorf("should be unchanged, got %q", got)
	}
}

func TestSanitize_PreservesNormalUnicode(t *testing.T) {
	input := "café résumé naïve"
	got := Sanitize(input)
	if got != input {
		t.Errorf("should be unchanged, got %q", got)
	}
}

func TestSanitize_StripsBOM(t *testing.T) {
	input := "\uFEFFhello"
	got := Sanitize(input)
	want := "hello"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSanitize_StripsDirectionalMarks(t *testing.T) {
	input := "hello\u200Eworld\u200F!"
	got := Sanitize(input)
	want := "helloworld!"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSanitize_BritishGasPattern(t *testing.T) {
	// real pattern from HTML emails: spaces interspersed with U+034F
	input := "  \u034F  \u034F  \u034F  \u034F  \u034F"
	got := Sanitize(input)
	want := "          "
	if got != want {
		t.Errorf("got %q (len %d), want %q (len %d)", got, len(got), want, len(want))
	}
}

// RFC 822 messages use CRLF line endings. If a stray \r survives into a
// rendered cell, the terminal interprets it as "move cursor to column 0"
// and every subsequent cell on that row lands at the wrong position —
// overwriting content on the same visual row (e.g. sidebar labels when
// a preview pane row contains \r). Always strip.
func TestSanitize_StripsCarriageReturns(t *testing.T) {
	input := "line one\r\nline two\r\nline three"
	got := Sanitize(input)
	want := "line one\nline two\nline three"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Isolated \r with no \n should also be stripped; not all content uses
// well-formed CRLF.
func TestSanitize_StripsBareCarriageReturn(t *testing.T) {
	input := "before\rafter"
	got := Sanitize(input)
	want := "beforeafter"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
