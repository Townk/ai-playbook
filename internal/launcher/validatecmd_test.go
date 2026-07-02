package launcher

import (
	"os"
	"path/filepath"
	"testing"

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
	if k, v, _, err := resolveValidateArgs([]string{"--file", "x.md"}); err != nil || k != "file" || v != "x.md" {
		t.Fatalf("--file: %s/%s err=%v", k, v, err)
	}
	if k, v, _, err := resolveValidateArgs([]string{"myslug"}); err != nil || k != "playbook" || v != "myslug" {
		t.Fatalf("slug: %s/%s err=%v", k, v, err)
	}
	if _, _, no, _ := resolveValidateArgs([]string{"--no-ai", "s"}); !no {
		t.Error("--no-ai must parse")
	}
	if _, _, _, err := resolveValidateArgs(nil); err == nil {
		t.Error("zero sources must error")
	}
	if _, _, _, err := resolveValidateArgs([]string{"s", "--file", "x.md"}); err == nil {
		t.Error("two sources must error")
	}
}

// ---- ValidateMain: clean vs error exit codes ----

func TestValidateMain_CleanVsError(t *testing.T) {
	defer swap(&reviewFn, func(_ *config.Config, _, _ string) (string, error) { return "looks good", nil })()

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
	defer swap(&reviewFn, func(_ *config.Config, _, _ string) (string, error) {
		called = true
		return "", nil
	})()

	clean := "---\nname: N\ndescription: D\ncategory: C\ncreated: 2026-01-01\n---\n\n# T\n\n```bash {id=a}\ntrue\n```\n"
	cleanPath := writeValidateTemp(t, "clean.md", clean)
	withArgs(t, []string{"ai-playbook", "validate", "--no-ai", "--file", cleanPath})
	if code := ValidateMain(); code != 0 || called {
		t.Fatalf("--no-ai must skip the AI pass (called=%v, code=%d)", called, code)
	}
}
