package compose

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
)

// SpellResult represents the result of checking a word
type SpellResult struct {
	Word        string
	Correct     bool
	Suggestions []string
}

// SpellChecker wraps aspell for async spell checking
type SpellChecker struct {
	cmd    *exec.Cmd
	stdin  *bufio.Writer
	stdout *bufio.Reader

	// channels for async communication - no mutex needed
	checkCh  chan string
	resultCh chan SpellResult
	closeCh  chan struct{}
}

// NewSpellChecker starts aspell and returns a ready checker
func NewSpellChecker(lang string) (*SpellChecker, error) {
	if lang == "" {
		lang = "en_GB"
	}

	cmd := exec.Command("aspell", "-a", "-l", lang)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("aspell stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("aspell stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("aspell start: %w", err)
	}

	sc := &SpellChecker{
		cmd:      cmd,
		stdin:    bufio.NewWriter(stdin),
		stdout:   bufio.NewReader(stdout),
		checkCh:  make(chan string, 1000),
		resultCh: make(chan SpellResult, 1000),
		closeCh:  make(chan struct{}),
	}

	// read and discard version line
	sc.stdout.ReadString('\n')

	// start worker - ONLY this goroutine talks to aspell
	go sc.worker()

	return sc, nil
}

// worker is the only goroutine that communicates with aspell
func (sc *SpellChecker) worker() {
	for {
		select {
		case <-sc.closeCh:
			return
		case word := <-sc.checkCh:
			Debug("spell: worker checking %q", word)
			result := sc.queryAspell(word)
			Debug("spell: worker got result for %q: correct=%v", word, result.Correct)
			// non-blocking send - drop if channel full
			select {
			case sc.resultCh <- result:
			default:
				Debug("spell: DROPPED result for %q (channel full)", word)
			}
		}
	}
}

// queryAspell does the actual aspell I/O - only called by worker goroutine
func (sc *SpellChecker) queryAspell(word string) SpellResult {
	result := SpellResult{Word: word, Correct: true}

	sc.stdin.WriteString(word + "\n")
	sc.stdin.Flush()

	line, err := sc.stdout.ReadString('\n')
	if err != nil {
		Debug("spell: aspell read error: %v", err)
		return result
	}
	line = strings.TrimSpace(line)

	switch {
	case line == "*":
		result.Correct = true
	case strings.HasPrefix(line, "&"):
		result.Correct = false
		if colonIdx := strings.Index(line, ":"); colonIdx > 0 && colonIdx < len(line)-1 {
			sugStr := strings.TrimSpace(line[colonIdx+1:])
			if sugStr != "" {
				result.Suggestions = strings.Split(sugStr, ", ")
			}
		}
	case strings.HasPrefix(line, "#"):
		result.Correct = false
	default:
		Debug("spell: unexpected aspell response: %q", line)
	}

	// consume blank line after response
	sc.stdout.ReadString('\n')

	return result
}

// Check queues a word for async checking (non-blocking)
func (sc *SpellChecker) Check(word string) {
	select {
	case sc.checkCh <- word:
	default:
		Debug("spell: DROPPED check for %q (queue full)", word)
	}
}

// Results returns the channel for receiving async results
func (sc *SpellChecker) Results() <-chan SpellResult {
	return sc.resultCh
}

// Close shuts down the spell checker
func (sc *SpellChecker) Close() error {
	close(sc.closeCh)
	sc.stdin.Flush()
	return sc.cmd.Process.Kill()
}

// IsAspellAvailable checks if aspell is installed
func IsAspellAvailable() bool {
	_, err := exec.LookPath("aspell")
	return err == nil
}

// SyncSpellChecker is a separate aspell process for synchronous queries
// (used for z= suggestions so it doesn't compete with async checker)
type SyncSpellChecker struct {
	cmd    *exec.Cmd
	stdin  *bufio.Writer
	stdout *bufio.Reader
}

// NewSyncSpellChecker creates a spell checker for synchronous operations
func NewSyncSpellChecker(lang string) (*SyncSpellChecker, error) {
	if lang == "" {
		lang = "en_GB"
	}

	cmd := exec.Command("aspell", "-a", "-l", lang)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("aspell stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("aspell stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("aspell start: %w", err)
	}

	sc := &SyncSpellChecker{
		cmd:    cmd,
		stdin:  bufio.NewWriter(stdin),
		stdout: bufio.NewReader(stdout),
	}

	// read and discard version line
	sc.stdout.ReadString('\n')

	return sc, nil
}

// Check checks a word synchronously and returns the result
func (sc *SyncSpellChecker) Check(word string) SpellResult {
	result := SpellResult{Word: word, Correct: true}

	sc.stdin.WriteString(word + "\n")
	sc.stdin.Flush()

	line, err := sc.stdout.ReadString('\n')
	if err != nil {
		return result
	}
	line = strings.TrimSpace(line)

	switch {
	case line == "*":
		result.Correct = true
	case strings.HasPrefix(line, "&"):
		result.Correct = false
		if colonIdx := strings.Index(line, ":"); colonIdx > 0 && colonIdx < len(line)-1 {
			sugStr := strings.TrimSpace(line[colonIdx+1:])
			if sugStr != "" {
				result.Suggestions = strings.Split(sugStr, ", ")
			}
		}
	case strings.HasPrefix(line, "#"):
		result.Correct = false
	}

	// consume blank line
	sc.stdout.ReadString('\n')

	return result
}

// AddWord adds a word to the personal dictionary
func (sc *SyncSpellChecker) AddWord(word string) error {
	// *word adds to personal dictionary
	sc.stdin.WriteString("*" + word + "\n")
	// # saves the dictionary
	sc.stdin.WriteString("#\n")
	return sc.stdin.Flush()
}

// Close shuts down the sync spell checker
func (sc *SyncSpellChecker) Close() error {
	sc.stdin.Flush()
	return sc.cmd.Process.Kill()
}
