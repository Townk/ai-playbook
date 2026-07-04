// tempfile.go — the shared "write content to a temp file, remove it on a
// write/close failure" helper used by every launcher path that hands ui.Run (or
// a spawned pane) a rendered body via a temp file.
package launcher

import "os"

// writeTempFile creates a temp file matching pattern (an os.CreateTemp pattern,
// e.g. "apb-answer-*.md" or "apb-request-*.json"), writes content to it, and
// returns its path. On a write or close failure the temp file is removed before
// the error is returned, so a caller never has to clean up a partially-written
// file itself.
//
// On SUCCESS, removal is the caller's call: a path handed to a process that
// reads it ASYNCHRONOUSLY (spawnAnswer, spawnSession — the spawned pane removes
// it itself, or the OS /tmp reap does) must NOT be removed here; a path read
// in-process before the caller returns (createViewPlaybook, viewProse,
// serveCachedPlaybook) is the caller's to `defer os.Remove(path)`.
func writeTempFile(pattern, content string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, werr := f.WriteString(content); werr != nil {
		f.Close()
		os.Remove(name)
		return "", werr
	}
	if cerr := f.Close(); cerr != nil {
		os.Remove(name)
		return "", cerr
	}
	return name, nil
}
