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
