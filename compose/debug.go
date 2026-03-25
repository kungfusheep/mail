package compose

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	debugEnabled bool
	debugMu      sync.Mutex
)

// EnableDebug turns on debug logging to stderr
func EnableDebug() {
	debugMu.Lock()
	debugEnabled = true
	debugMu.Unlock()
}

// Debug logs a message to stderr if debug mode is enabled
func Debug(format string, args ...any) {
	debugMu.Lock()
	enabled := debugEnabled
	debugMu.Unlock()
	if !enabled {
		return
	}
	timestamp := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stderr, "[%s] %s\n", timestamp, fmt.Sprintf(format, args...))
}
