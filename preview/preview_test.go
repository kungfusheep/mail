package preview

import "testing"

func TestRenderHTMLFallback(t *testing.T) {
	// when html is empty, should return fallback text
	result := RenderHTML("", "plain text fallback")
	if result != "plain text fallback" {
		t.Errorf("expected fallback text, got %q", result)
	}
}

func TestRenderToLinesEmpty(t *testing.T) {
	lines := RenderToLines("", "")
	if lines != nil {
		t.Errorf("expected nil for empty input, got %v", lines)
	}
}

func TestRenderToLinesFallback(t *testing.T) {
	lines := RenderToLines("", "line one\nline two")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
}
