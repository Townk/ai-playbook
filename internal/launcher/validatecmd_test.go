package launcher

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/askbridge"
	"github.com/Townk/ai-playbook/internal/config"
)

// swap replaces *target with fn for the duration of the test, returning a
// restore func for `defer`. Generic over the launcher package's function-var
// seams (reviewFn, storeLoadFn, uiMainFn, …).
func swap[T any](target *T, fn T) func() {
	old := *target
	*target = fn
	return func() { *target = old }
}

// writeValidateTemp writes content to a temp .md file under t.TempDir() and
// returns its path.
func writeValidateTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- resolveValidateArgs ----

func TestResolveValidateArgs(t *testing.T) {
	if ra, err := resolveValidateArgs([]string{"--file", "x.md"}); err != nil || ra.Kind != "file" || ra.Value != "x.md" {
		t.Fatalf("--file: %+v err=%v", ra, err)
	}
	if ra, err := resolveValidateArgs([]string{"myslug"}); err != nil || ra.Kind != "playbook" || ra.Value != "myslug" {
		t.Fatalf("slug: %+v err=%v", ra, err)
	}
	if ra, _ := resolveValidateArgs([]string{"--no-ai", "s"}); !ra.NoAI {
		t.Error("--no-ai must parse")
	}
	if ra, _ := resolveValidateArgs([]string{"--plain", "s"}); !ra.Plain {
		t.Error("--plain must parse")
	}
	if ra, _ := resolveValidateArgs([]string{"--quiet", "s"}); !ra.Quiet {
		t.Error("--quiet must parse")
	}
	if ra, _ := resolveValidateArgs([]string{"--plain", "--quiet", "s"}); !ra.Plain || !ra.Quiet {
		t.Errorf("--plain --quiet must both parse: %+v", ra)
	}
	if _, err := resolveValidateArgs(nil); err == nil {
		t.Error("zero sources must error")
	}
	if _, err := resolveValidateArgs([]string{"s", "--file", "x.md"}); err == nil {
		t.Error("two sources must error")
	}
}

// ---- ValidateMain: clean vs error exit codes ----

func TestValidateMain_CleanVsError(t *testing.T) {
	defer swap(&reviewStreamFn, func(_ *config.Config, _, _ string) (<-chan agentstream.Event, func() error, error) {
		return canned("looks good"), noopClose, nil
	})()
	defer swap(&runCreateProgressFn, func(_ <-chan string, _ *askbridge.Bridge, done <-chan struct{}) { <-done })()

	clean := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	cleanPath := writeValidateTemp(t, "clean.md", clean)
	withArgs(t, []string{"ai-playbook", "validate", "--file", cleanPath})
	if code := ValidateMain(); code != 0 {
		t.Fatalf("clean → exit %d, want 0", code)
	}

	bad := "---\nname: N\ndescription: D\ncategory: C\ncreated: x\n---\n\n# T\n\n```bash {id=a needs=ghost}\ntrue\n```\n"
	badPath := writeValidateTemp(t, "bad.md", bad)
	withArgs(t, []string{"ai-playbook", "validate", "--file", badPath})
	if code := ValidateMain(); code != 1 {
		t.Fatalf("dangling needs → exit %d, want 1", code)
	}
}

// ---- ValidateMain: --no-ai skips the AI pass entirely ----

func TestValidateMain_NoAISkip(t *testing.T) {
	var called bool
	defer swap(&reviewStreamFn, func(_ *config.Config, _, _ string) (<-chan agentstream.Event, func() error, error) {
		called = true
		return nil, nil, nil
	})()

	clean := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	cleanPath := writeValidateTemp(t, "clean.md", clean)
	withArgs(t, []string{"ai-playbook", "validate", "--no-ai", "--file", cleanPath})
	if code := ValidateMain(); code != 0 || called {
		t.Fatalf("--no-ai must skip the AI pass (called=%v, code=%d)", called, code)
	}
}

// TestValidateMain_NoAISkipsStream is the brief's dedicated --no-ai seam-not-called
// assertion (distinct name from TestValidateMain_NoAISkip, kept alongside it since
// both already cover the same behavior end-to-end).
func TestValidateMain_NoAISkipsStream(t *testing.T) {
	var called bool
	defer swap(&reviewStreamFn, func(_ *config.Config, _, _ string) (<-chan agentstream.Event, func() error, error) {
		called = true
		return nil, nil, nil
	})()

	clean := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	cleanPath := writeValidateTemp(t, "clean.md", clean)
	withArgs(t, []string{"ai-playbook", "validate", "--no-ai", "--file", cleanPath})
	if code := ValidateMain(); code != 0 {
		t.Fatalf("--no-ai clean file → exit %d, want 0", code)
	}
	if called {
		t.Fatal("--no-ai must not call the review stream")
	}
}

// ---- ValidateMain: AI pass streams + captures text via the new seam ----

// canned returns a closed agentstream.Event channel carrying a single Final
// event whose text is text — the shape ReviewStream's real callers drain
// (mirrors internal/author/review_test.go's drainReviewText contract).
func canned(text string) <-chan agentstream.Event {
	ch := make(chan agentstream.Event, 1)
	ch <- agentstream.Event{Kind: agentstream.Final, Text: text}
	close(ch)
	return ch
}

// noopClose is a canned reviewStreamFn's closeFn: FanOut calls it once the
// pump drains the event channel, so it must be non-nil and side-effect-free.
func noopClose() error { return nil }

func TestValidateMain_AITextCaptured(t *testing.T) {
	defer swap(&reviewStreamFn, func(_ *config.Config, _, _ string) (<-chan agentstream.Event, func() error, error) {
		return canned("looks good"), noopClose, nil
	})()
	defer swap(&runCreateProgressFn, func(_ <-chan string, _ *askbridge.Bridge, done <-chan struct{}) { <-done })()

	clean := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	cleanPath := writeValidateTemp(t, "clean.md", clean)
	withArgs(t, []string{"ai-playbook", "validate", "--file", cleanPath})

	var code int
	out := captureStdout(t, func() { code = ValidateMain() })
	if code != 0 {
		t.Fatalf("clean → exit %d, want 0", code)
	}
	if !strings.Contains(out, "looks good") {
		t.Fatalf("AI review text not in report:\n%s", out)
	}
}

// ---- ValidateMain: --quiet suppresses all output and skips the AI pass ----

func TestValidateMain_Quiet_SilentExitCodeOnly(t *testing.T) {
	var called bool
	defer swap(&reviewStreamFn, func(_ *config.Config, _, _ string) (<-chan agentstream.Event, func() error, error) {
		called = true
		return canned("looks good"), noopClose, nil
	})()

	clean := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	cleanPath := writeValidateTemp(t, "clean.md", clean)
	withArgs(t, []string{"ai-playbook", "validate", "--quiet", "--file", cleanPath})

	var code int
	out := captureStdout(t, func() { code = ValidateMain() })
	if code != 0 {
		t.Fatalf("clean --quiet → exit %d, want 0", code)
	}
	if out != "" {
		t.Fatalf("--quiet must print nothing, got %q", out)
	}
	if called {
		t.Error("--quiet must skip the AI pass (reviewStreamFn must not be called)")
	}

	bad := "---\nname: N\ndescription: D\ncategory: C\ncreated: x\n---\n\n# T\n\n```bash {id=a needs=ghost}\ntrue\n```\n"
	badPath := writeValidateTemp(t, "bad.md", bad)
	withArgs(t, []string{"ai-playbook", "validate", "--quiet", "--file", badPath})

	called = false
	out = captureStdout(t, func() { code = ValidateMain() })
	if code != 1 {
		t.Fatalf("dangling needs --quiet → exit %d, want 1", code)
	}
	if out != "" {
		t.Fatalf("--quiet must print nothing on error, got %q", out)
	}
	if called {
		t.Error("--quiet must skip the AI pass even on structural errors")
	}
}

// ---- ValidateMain: --plain forces the dot progress even on a TTY ----

func TestValidateMain_PlainForcesDots(t *testing.T) {
	defer swap(&hasTTYFn, func() bool { return true })()
	defer swap(&reviewStreamFn, func(_ *config.Config, _, _ string) (<-chan agentstream.Event, func() error, error) {
		return canned("looks good"), noopClose, nil
	})()

	clean := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	cleanPath := writeValidateTemp(t, "clean.md", clean)

	var spinnerCalled bool
	restoreProgress := swap(&runCreateProgressFn, func(_ <-chan string, _ *askbridge.Bridge, done <-chan struct{}) {
		spinnerCalled = true
		<-done
	})

	withArgs(t, []string{"ai-playbook", "validate", "--plain", "--file", cleanPath})
	if code := ValidateMain(); code != 0 {
		t.Fatalf("--plain clean → exit %d, want 0", code)
	}
	if spinnerCalled {
		t.Error("--plain must NOT call runCreateProgressFn (dots path must be taken)")
	}
	restoreProgress()

	spinnerCalled = false
	restoreProgress = swap(&runCreateProgressFn, func(_ <-chan string, _ *askbridge.Bridge, done <-chan struct{}) {
		spinnerCalled = true
		<-done
	})
	defer restoreProgress()

	withArgs(t, []string{"ai-playbook", "validate", "--file", cleanPath})
	if code := ValidateMain(); code != 0 {
		t.Fatalf("clean (no --plain) → exit %d, want 0", code)
	}
	if !spinnerCalled {
		t.Error("without --plain (TTY stubbed true) runCreateProgressFn must be called (spinner path)")
	}
}

// ---- heartbeat: pure dots-then-newline, no TTY/model needed ----

func TestHeartbeat_DotsThenNewline(t *testing.T) {
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { time.Sleep(120 * time.Millisecond); close(done) }()
	heartbeat(&buf, done, 40*time.Millisecond) // >=2 ticks before done
	out := buf.String()
	if !strings.Contains(out, ".") {
		t.Fatalf("expected dots, got %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("expected trailing newline, got %q", out)
	}
}
