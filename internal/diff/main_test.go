package diff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffMain_RendersPatchFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "p.patch")
	os.WriteFile(f, []byte("--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n"), 0o644)
	// renderFile is the headless core Main wraps (Main runs the TUI; test the core).
	out := renderFile(f, 100)
	if !strings.Contains(out, "old") || !strings.Contains(out, "new") {
		t.Fatalf("renderFile output:\n%s", out)
	}
}
