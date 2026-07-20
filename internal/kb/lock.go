package kb

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// WithFileLock serializes a knowledge-file read-modify-write across processes:
// an exclusive flock(2) on the sidecar `<path>.lock`, held for fn's duration.
// Concurrent `remember` calls from different sessions previously interleaved
// ReadFile→WriteFile and the LATER write silently dropped the earlier session's
// fact. The sidecar (not the knowledge file itself) carries the lock so the
// file can be replaced while held and a not-yet-existing file locks fine.
// Unix-only (flock), like the rest of the product (the driver needs a Unix PTY).
func WithFileLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := unix.Flock(int(lf.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = unix.Flock(int(lf.Fd()), unix.LOCK_UN) }()
	return fn()
}
