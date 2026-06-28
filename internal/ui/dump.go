package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// dumpDocument writes the viewer's current raw document (md) verbatim to a
// timestamped file under /tmp, returning the path (or "" on failure). It is a
// HIDDEN debug affordance (bound to ctrl+x in the viewer): when a render looks
// wrong, press it to capture the exact source the renderer received so the issue
// can be reproduced. Not shown in the help modal.
func dumpDocument(md string) string {
	path := filepath.Join("/tmp", fmt.Sprintf("apb-doc-dump-%d.md", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		return ""
	}
	return path
}
