package autorun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteRunLog_WritesJSON(t *testing.T) {
	dir := t.TempDir()
	results := []StepResult{
		{ID: "build", Command: "b.sh", Exit: 0, Status: StatusOK},
		{ID: "test", Command: "t.sh", Exit: 1, Status: StatusFailed},
	}
	path, err := WriteRunLog(dir, "20260701T120000Z", "seven", results)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "runs", "20260701T120000Z-seven.json"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"id": "test"`) || !strings.Contains(string(raw), `"exit": 1`) {
		t.Fatalf("log missing step records:\n%s", raw)
	}
}

func TestSummarize_RendersEachStatus(t *testing.T) {
	out := Summarize([]StepResult{
		{ID: "build", Status: StatusOK, Exit: 0},
		{ID: "test", Status: StatusFailed, Exit: 1},
		{ID: "status", Status: StatusSkipped},
	})
	for _, want := range []string{"build", "test", "status", "exit 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

// A timed-out step's summary row names the effective ceiling instead of
// reading as a plain failure; a plain failure keeps its existing form.
func TestSummarize_TimedOutRow(t *testing.T) {
	out := Summarize([]StepResult{
		{ID: "slow", Status: StatusFailed, Exit: 143, TimedOutAfter: "1s"},
		{ID: "boom", Status: StatusFailed, Exit: 1},
	})
	if !strings.Contains(out, "✗ failed") {
		t.Errorf("timed-out row keeps the failed status word:\n%s", out)
	}
	if !strings.Contains(out, "(timed out after 1s, exit 143)") {
		t.Errorf("timed-out row must name the effective ceiling:\n%s", out)
	}
	if !strings.Contains(out, "(exit 1)") {
		t.Errorf("plain failure row changed:\n%s", out)
	}
}

// TestWriteJUnit pins the JUnit-XML report shape: one testsuite named after
// the slug, a plain pass for ok, <failure> for failed (message carries exit /
// timeout, body the command + output path), <skipped> for the non-failure
// terminal statuses, and the suite counters/time derived from the results.
func TestWriteJUnit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reports", "run.xml")
	results := []StepResult{
		{ID: "install", Command: "make install", Status: StatusOK, Exit: 0, Duration: 1500 * time.Millisecond},
		{ID: "verify", Command: "make verify", Status: StatusFailed, Exit: 2, OutputPath: "/tmp/verify.log", Duration: 250 * time.Millisecond},
		{ID: "hang", Command: "sleep 99", Status: StatusFailed, Exit: 1, TimedOutAfter: "10s"},
		{ID: "cleanup", Command: "rm -f x", Status: StatusSkipped},
	}
	if err := WriteJUnit(path, "deploy-staging", "20260719T120000Z", results); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		`<testsuite name="deploy-staging" tests="4" failures="2" skipped="1"`,
		`timestamp="2026-07-19T12:00:00Z"`,
		`<testcase name="install" classname="deploy-staging" time="1.500"`,
		`<failure message="exit 2">make verify&#xA;output: /tmp/verify.log</failure>`,
		`<failure message="timed out after 10s (exit 1)">`,
		`<skipped message="skipped">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("junit report missing %q:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "<?xml") {
		t.Error("report must start with the XML header")
	}
}
