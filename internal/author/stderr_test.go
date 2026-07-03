package author

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/config"
)

func writeFakeClaude(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// harnessCfg builds a config pinned to the claude harness with Bin overridden to
// the fake binary — the events path resolves the process from cfg, not env.
func harnessCfg(bin string) *config.Config {
	cfg := config.Default()
	cfg.Agent.Harness = "claude"
	cfg.Agent.Bin = bin
	return cfg
}

// On a successful run the harness's stderr (e.g. claude's untrusted-workspace
// warnings) is captured, NOT leaked to os.Stderr — so it can't pollute the
// no-mux inline UI. Regression guard for the live "ton of warnings in the inline
// box" report, now enforced on the events-backed Agent (HarnessAgent).
func TestHarnessAgent_StderrNotLeakedOnSuccess(t *testing.T) {
	// Emit one stream-json text_delta ("ok") + a stderr warning, exit 0.
	bin := writeFakeClaude(t, "#!/bin/sh\n"+
		"echo 'WARN untrusted workspace, ignoring 10 entries' >&2\n"+
		`printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}}'`+"\n"+
		"exit 0\n")

	// Redirect os.Stderr to prove the harness chatter never reaches it.
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	restore := func() { os.Stderr = old }

	agent := HarnessAgent(AuthorOptions{Cfg: harnessCfg(bin)})
	rc, err := agent("sys", "user")
	if err != nil {
		restore()
		t.Fatalf("HarnessAgent: %v", err)
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
// diagnostic. The events path joins it into wait(), which the Agent returns from Close.
func TestHarnessAgent_StderrSurfacedOnFailure(t *testing.T) {
	bin := writeFakeClaude(t, "#!/bin/sh\necho 'boom: real failure detail' >&2\nexit 7\n")

	agent := HarnessAgent(AuthorOptions{Cfg: harnessCfg(bin)})
	rc, err := agent("sys", "user")
	if err != nil {
		t.Fatalf("HarnessAgent start: %v", err)
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
