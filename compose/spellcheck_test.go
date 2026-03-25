package compose

import (
	"testing"
	"time"
)

func TestSyncSpellChecker(t *testing.T) {
	if !IsAspellAvailable() {
		t.Skip("aspell not installed")
	}

	sc, err := NewSyncSpellChecker("en_GB")
	if err != nil {
		t.Fatalf("failed to create spell checker: %v", err)
	}
	defer sc.Close()

	tests := []struct {
		word    string
		correct bool
	}{
		{"hello", true},
		{"world", true},
		{"the", true},
		{"helo", false},
		{"wrold", false},
		{"teh", false},
	}

	for _, tt := range tests {
		result := sc.Check(tt.word)
		if result.Correct != tt.correct {
			t.Errorf("Check(%q) = %v, want %v", tt.word, result.Correct, tt.correct)
		}
		if !result.Correct && len(result.Suggestions) == 0 {
			t.Logf("Check(%q) has no suggestions", tt.word)
		}
	}
}

func TestSyncSpellCheckerSuggestions(t *testing.T) {
	if !IsAspellAvailable() {
		t.Skip("aspell not installed")
	}

	sc, err := NewSyncSpellChecker("en_GB")
	if err != nil {
		t.Fatalf("failed to create spell checker: %v", err)
	}
	defer sc.Close()

	result := sc.Check("helo")
	if result.Correct {
		t.Error("expected 'helo' to be incorrect")
	}
	if len(result.Suggestions) == 0 {
		t.Error("expected suggestions for 'helo'")
	}

	// check that "hello" is in suggestions
	found := false
	for _, s := range result.Suggestions {
		if s == "hello" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'hello' in suggestions, got %v", result.Suggestions)
	}
}

func TestAsyncSpellChecker(t *testing.T) {
	if !IsAspellAvailable() {
		t.Skip("aspell not installed")
	}

	sc, err := NewSpellChecker("en_GB")
	if err != nil {
		t.Fatalf("failed to create spell checker: %v", err)
	}
	defer sc.Close()

	// queue some words
	words := []string{"hello", "helo", "world", "wrold"}
	for _, w := range words {
		sc.Check(w)
	}

	// collect results with timeout
	results := make(map[string]bool)
	timeout := time.After(2 * time.Second)
	for len(results) < len(words) {
		select {
		case result := <-sc.Results():
			results[result.Word] = result.Correct
		case <-timeout:
			t.Fatalf("timeout waiting for results, got %d of %d", len(results), len(words))
		}
	}

	// verify results
	expected := map[string]bool{
		"hello": true,
		"helo":  false,
		"world": true,
		"wrold": false,
	}
	for word, correct := range expected {
		if results[word] != correct {
			t.Errorf("word %q: got correct=%v, want %v", word, results[word], correct)
		}
	}
}
