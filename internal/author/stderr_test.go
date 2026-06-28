package author

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeClaude(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// On a successful run the harness's stderr (e.g. claude's untrusted-workspace
// warnings) is captured, NOT leaked to os.Stderr — so it can't pollute the
// no-mux inline UI. Regression guard for the live "ton of warnings in the inline
// box" report.
func TestRunClaude_StderrNotLeakedOnSuccess(t *testing.T) {
	bin := writeFakeClaude(t, "#!/bin/sh\necho 'WARN untrusted workspace, ignoring 10 entries' >&2\necho ok\nexit 0\n")
	t.Setenv("AI_PLAYBOOK_CLAUDE_BIN", bin)

	// Redirect os.Stderr to prove the harness chatter never reaches it.
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	restore := func() { os.Stderr = old }

	rc, err := runClaude("sys", "user", nil)
	if err != nil {
		restore()
		t.Fatalf("runClaude: %v", err)
	}
	out, _ := io.ReadAll(rc)
	closeErr := rc.Close()
	_ = w.Close()
	restore()
	leaked, _ := io.ReadAll(r)

	if closeErr != nil {
		t.Fatalf("success Close should be nil, got %v", closeErr)
	}
	if strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("stdout = %q, want %q", out, "ok")
	}
	if strings.Contains(string(leaked), "WARN") {
		t.Errorf("harness stderr leaked to os.Stderr on success: %q", leaked)
	}
}

// On failure the captured stderr IS surfaced in the error so real problems stay
// diagnostic.
func TestRunClaude_StderrSurfacedOnFailure(t *testing.T) {
	bin := writeFakeClaude(t, "#!/bin/sh\necho 'boom: real failure detail' >&2\nexit 7\n")
	t.Setenv("AI_PLAYBOOK_CLAUDE_BIN", bin)

	rc, err := runClaude("sys", "user", nil)
	if err != nil {
		t.Fatalf("runClaude start: %v", err)
	}
	_, _ = io.ReadAll(rc)
	closeErr := rc.Close()
	if closeErr == nil {
		t.Fatal("a non-zero exit must return an error from Close")
	}
	if !strings.Contains(closeErr.Error(), "boom: real failure detail") {
		t.Errorf("failure error must carry the captured stderr tail, got: %v", closeErr)
	}
}
