package autorun

import (
	"os/exec"
	"strings"
	"testing"
)

// NextRunnable must fold from= into the effective-needs order: a consumer that
// declares from=prod (even with no textual needs=, and even placed first in
// document order) is NOT runnable until prod is ok — prod is surfaced first.
// RED against the pre-from= NextRunnable, which only consulted b.Needs and so
// would return the consumer immediately.
func TestNextRunnable_FromOrdersProducerFirst(t *testing.T) {
	blocks := []Block{
		{ID: "cons", Kind: KindRun, Command: "cat", From: "prod"}, // first in doc order
		{ID: "prod", Kind: KindRun, Command: "printf DATA"},
	}
	status := map[string]string{}

	b, ok := NextRunnable(blocks, status)
	if !ok || b.ID != "prod" {
		t.Fatalf("first runnable = %q ok=%v, want prod (from= producer materializes first)", b.ID, ok)
	}

	status["prod"] = StatusOK
	b, ok = NextRunnable(blocks, status)
	if !ok || b.ID != "cons" {
		t.Fatalf("after prod ok, runnable = %q ok=%v, want cons", b.ID, ok)
	}
}

// A consumer whose from= producer was skipped/failed is itself never runnable —
// the data edge gates exactly like a needs= edge.
func TestNextRunnable_FromProducerNotOk_Gates(t *testing.T) {
	blocks := []Block{
		{ID: "prod", Kind: KindRun, Command: "printf DATA"},
		{ID: "cons", Kind: KindRun, Command: "cat", From: "prod"},
	}
	if _, ok := NextRunnable(blocks, map[string]string{"prod": StatusSkipped}); ok {
		t.Fatal("consumer must not be runnable while its from= producer is skipped")
	}
}

// End-to-end headless pipe: a producer's stdout is retained and fed into the
// consumer's stdin (via RunStep → CapturePath → StdinPath). The consumer `cat`
// emits the producer's exact bytes, proving the whole --auto piping path.
func TestRun_Piped_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a real driver")
	}
	var out strings.Builder
	code := Run(RunConfig{
		Blocks: []Block{
			{ID: "prod", Kind: KindRun, Command: "printf HELLO_PIPE"},
			{ID: "cons", Kind: KindRun, Command: "cat", From: "prod"},
		},
		Slug: "t", Out: &out, Now: func() string { return "STAMP" },
	})
	if code != 0 {
		t.Fatalf("piped run exit = %d, want 0\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "HELLO_PIPE") {
		t.Fatalf("consumer did not receive producer's piped stdout:\n%s", out.String())
	}
}

// A script (run) consumer reading sys.stdin gets the producer's bytes — the
// flagship python case, end-to-end through the headless path.
func TestRun_PipedPythonFilter_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a real driver")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	var out strings.Builder
	code := Run(RunConfig{
		Blocks: []Block{
			{ID: "prod", Kind: KindRun, Command: "printf 'line1\\nline2'"},
			{ID: "filter", Kind: KindRun, Lang: "python", From: "prod",
				Command: "import sys\nfor n, l in enumerate(sys.stdin, 1):\n    print(f'{n}:{l.rstrip()}')"},
		},
		Slug: "t", Out: &out, Now: func() string { return "STAMP" },
	})
	if code != 0 {
		t.Fatalf("piped python run exit = %d, want 0\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "1:line1") || !strings.Contains(out.String(), "2:line2") {
		t.Fatalf("python filter did not read producer's stdin:\n%s", out.String())
	}
}

// TestRun_ProducePythonFilterConsume_EndToEnd is the Phase 6 close-out
// end-to-end pin (v0.11 P5): the flagship three-block pipeline — produce →
// python filter (reads sys.stdin) → consume — through the real headless
// `--auto` path (autorun.Run: real driver, real zsh, no fakes). Proves the
// FULL chain composes: the producer's raw bytes reach the python filter's
// stdin, and the filter's transformed stdout in turn reaches the consumer's
// stdin, entirely via from= edges and NextRunnable's topological ordering —
// no explicit needs= anywhere in the document.
func TestRun_ProducePythonFilterConsume_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a real driver")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	var out strings.Builder
	code := Run(RunConfig{
		Blocks: []Block{
			{ID: "prod", Kind: KindRun, Command: "printf 'a\\nb\\nc'"},
			{ID: "filter", Kind: KindRun, Lang: "python", From: "prod",
				Command: "import sys\nfor l in sys.stdin:\n    print(l.strip().upper())"},
			{ID: "cons", Kind: KindRun, Command: "cat", From: "filter"},
		},
		Slug: "t", Out: &out, Now: func() string { return "STAMP" },
	})
	if code != 0 {
		t.Fatalf("piped run exit = %d, want 0\n%s", code, out.String())
	}
	for _, want := range []string{"A", "B", "C"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("consumer output missing filtered/uppercased %q:\n%s", want, out.String())
		}
	}
}
