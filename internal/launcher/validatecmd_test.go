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
	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/pkg/store"
)

// swap replaces *target with fn for the duration of the test, returning a
// restore func for `defer`. Generic over the launcher package's function-var
// seams (reviewFn, storeLoadFn, uiRunFn, …).
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

// ---- ValidateMain: depends_on chain findings ----

// TestValidateMain_DanglingDependsOn verifies a playbook whose depends_on
// names a slug missing from the store gets a depends_on Error finding and
// exits 1 (folded into the same structural exit code as any other Error).
func TestValidateMain_DanglingDependsOn(t *testing.T) {
	defer swap(&reviewStreamFn, func(_ *config.Config, _, _ string) (<-chan agentstream.Event, func() error, error) {
		return canned("looks good"), noopClose, nil
	})()
	defer swap(&runCreateProgressFn, func(_ <-chan string, _ *askbridge.Bridge, done <-chan struct{}) { <-done })()
	defer swap(&storePathForFn, func(string) (string, bool) { return "", false })()

	pb := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\ndepends_on:\n  - ghost\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	path := writeValidateTemp(t, "parent.md", pb)
	withArgs(t, []string{"ai-playbook", "validate", "--file", path})

	var code int
	out := captureStdout(t, func() { code = ValidateMain() })
	if code != 1 {
		t.Fatalf("dangling depends_on → exit %d, want 1", code)
	}
	if !strings.Contains(out, "depends_on") || !strings.Contains(out, `"ghost" does not exist in the store`) {
		t.Fatalf("missing depends_on finding in report:\n%s", out)
	}
}

// TestValidateMain_DependsOnCycle verifies a depends_on cycle reachable
// through the store gets a cycle Error finding and exits 1.
func TestValidateMain_DependsOnCycle(t *testing.T) {
	defer swap(&reviewStreamFn, func(_ *config.Config, _, _ string) (<-chan agentstream.Event, func() error, error) {
		return canned("looks good"), noopClose, nil
	})()
	defer swap(&runCreateProgressFn, func(_ <-chan string, _ *askbridge.Bridge, done <-chan struct{}) { <-done })()

	dir := t.TempDir()
	aPath := writeDepPlaybook(t, dir, "a", "depends_on:\n  - b\n")
	bPath := writeDepPlaybook(t, dir, "b", "depends_on:\n  - a\n")
	defer swap(&storePathForFn, func(slug string) (string, bool) {
		switch slug {
		case "a":
			return aPath, true
		case "b":
			return bPath, true
		}
		return "", false
	})()

	pb := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\ndepends_on:\n  - a\n---\n\n# T\n\n```bash {id=x}\ntrue\n```\n"
	path := writeValidateTemp(t, "parent.md", pb)
	withArgs(t, []string{"ai-playbook", "validate", "--file", path})

	var code int
	out := captureStdout(t, func() { code = ValidateMain() })
	if code != 1 {
		t.Fatalf("depends_on cycle → exit %d, want 1", code)
	}
	if !strings.Contains(out, "depends_on cycle:") {
		t.Fatalf("missing cycle finding in report:\n%s", out)
	}
}

// TestValidateMain_StoredSlug_ReadsFullFile is the regression lock-in for the
// stored-slug front-matter-drop bug: store.Load's second return value (body)
// is front-matter-STRIPPED, so a "playbook" branch that set content = body
// (the old code) fed frontmatter.Parse a document with no front matter at
// all — frontmatter.Parse's `ok` came back false, validate.Check raised a
// false "missing or malformed front matter" error, and the depends_on chain
// (which reads fm.DependsOn) never ran at all. The fix re-reads meta.Path
// (the FULL file) instead. Without the fix this test fails: the report
// contains "missing or malformed front matter" and omits the depends_on
// finding.
func TestValidateMain_StoredSlug_ReadsFullFile(t *testing.T) {
	defer swap(&storePathForFn, func(string) (string, bool) { return "", false })() // "ghost" unresolvable

	pb := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\ndepends_on:\n  - ghost\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	path := writeValidateTemp(t, "stored.md", pb)
	// strippedBody mimics store.Load's real second return value: the SAME
	// file with its front matter removed — distinct from the full file at
	// meta.Path, so a regression back to `content = body` is caught.
	strippedBody := "\n# T\n\n```bash {id=a}\ntrue\n```\n"
	defer swap(&storeLoadFn, func(string) (store.Meta, string, error) {
		return store.Meta{Path: path}, strippedBody, nil
	})()

	withArgs(t, []string{"ai-playbook", "validate", "--no-ai", "myslug"})
	var code int
	out := captureStdout(t, func() { code = ValidateMain() })
	if code != 1 {
		t.Fatalf("ValidateMain exit %d, want 1 (dangling depends_on)", code)
	}
	if strings.Contains(out, "missing or malformed front matter") {
		t.Fatalf("false front-matter error: front matter must parse from the FULL stored file, got:\n%s", out)
	}
	if !strings.Contains(out, "depends_on") || !strings.Contains(out, `"ghost" does not exist in the store`) {
		t.Fatalf("missing depends_on finding in report (front matter must be parsed to see depends_on at all):\n%s", out)
	}
}

// ---- reviewSystemPrompt: rubric-fed AI review ----

// TestValidateMain_ReviewPromptCarriesRubric pins the AI review pass to the
// authoring rubric: the system prompt ValidateMain hands the review stream
// embeds author.AuthoringRubric() byte-identical (the review judges against
// the SAME quality bar the authoring prompts teach — single source, no
// drift), asks for the judgment calls the mechanical quality checks
// deliberately skip (atomicity/coarseness, per-step rollback need), and keeps
// the brevity/no-nitpicks instruction.
func TestValidateMain_ReviewPromptCarriesRubric(t *testing.T) {
	var gotSys string
	defer swap(&reviewStreamFn, func(_ *config.Config, sysPrompt, _ string) (<-chan agentstream.Event, func() error, error) {
		gotSys = sysPrompt
		return canned("looks good"), noopClose, nil
	})()
	defer swap(&runCreateProgressFn, func(_ <-chan string, _ *askbridge.Bridge, done <-chan struct{}) { <-done })()

	clean := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	cleanPath := writeValidateTemp(t, "clean.md", clean)
	withArgs(t, []string{"ai-playbook", "validate", "--file", cleanPath})
	captureStdout(t, func() { ValidateMain() })

	if !strings.Contains(gotSys, author.AuthoringRubric()) {
		t.Errorf("review system prompt must embed author.AuthoringRubric() verbatim:\n%s", gotSys)
	}
	for _, want := range []string{"atomic", "rollback", "coarse", "inventing nitpicks"} {
		if !strings.Contains(gotSys, want) {
			t.Errorf("review system prompt missing %q:\n%s", want, gotSys)
		}
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
