package ui

import (
	"fmt"
	"os"
	"time"
)

// dbgFile is the open append handle for AI_ASSIST_DEBUG_LOG, or nil when the
// env var is unset/empty or the file could not be opened. Resolved once at
// package init so dbg() is a cheap no-op in the common (unset) case.
var dbgFile *os.File

func init() { resolveDbg() }

// resolveDbg (re)resolves dbgFile from AI_ASSIST_DEBUG_LOG: opens the file for
// append when the var is non-empty and openable, else leaves dbgFile nil so dbg
// is a no-op. Called once at init; also reusable from tests that toggle the env.
func resolveDbg() {
	dbgFile = nil
	path := os.Getenv("AI_ASSIST_DEBUG_LOG")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	dbgFile = f
}

// dbg appends a timestamped line "<RFC3339> [pager] <msg>\n" to the file named
// by AI_ASSIST_DEBUG_LOG. It is a no-op when that env var is unset/empty or the
// file failed to open. Safe to call from the single-goroutine bubbletea Update
// loop; not designed for concurrent callers.
func dbg(format string, args ...any) {
	if dbgFile == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(dbgFile, "%s [pager] %s\n", time.Now().Format(time.RFC3339), msg)
}

// SetDebugLog points the ui trace at path. The session pane receives the path as
// a --debug-log FLAG (the zellij spawn drops env vars), so it must set the env +
// reopen the handle for the ui's dbg() to write. No-op for empty path.
func SetDebugLog(path string) {
	if path == "" {
		return
	}
	os.Setenv("AI_ASSIST_DEBUG_LOG", path)
	resolveDbg()
}
