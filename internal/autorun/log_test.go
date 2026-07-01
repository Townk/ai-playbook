package autorun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
