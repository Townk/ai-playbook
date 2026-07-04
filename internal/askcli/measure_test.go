package askcli

import (
	"os"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/pkg/dialog"
)

// TestMeasureParityConfirm asserts that `ask confirm --measure` reports the same
// rendered height as `ai-playbook input --type confirm --measure` for an
// identical dialog. Both are driven in-process; --measure needs no TTY.
func TestMeasureParityConfirm(t *testing.T) {
	const (
		prompt = "Proceed with the migration?"
		width  = "44"
	)

	// ask path.
	askCode, askOut, _ := runAsk("confirm", prompt, "--measure", "--width", width)
	if askCode != 0 {
		t.Fatalf("ask exit = %d", askCode)
	}

	// ai-playbook input path (drive dialog.Main via os.Args).
	origArgs := os.Args
	os.Args = []string{"ai-playbook", "input", "--type", "confirm", "--prompt", prompt, "--measure", "--width", width}
	inCode, inOut, _ := capture(func() int { return dialog.Main() })
	os.Args = origArgs
	if inCode != 0 {
		t.Fatalf("input exit = %d", inCode)
	}

	if strings.TrimSpace(askOut) != strings.TrimSpace(inOut) {
		t.Errorf("measure mismatch: ask=%q input=%q", askOut, inOut)
	}
	if strings.TrimSpace(askOut) == "" {
		t.Fatalf("measure produced empty output")
	}
}

// TestMeasureNoTTY confirms --measure runs without a terminal (no widget seam,
// no hasTTY gate).
func TestMeasureNoTTY(t *testing.T) {
	origTTY := hasTTY
	hasTTY = func() bool { return false }
	defer func() { hasTTY = origTTY }()
	code, out, _ := runAsk("line", "Name?", "--measure")
	if code != 0 || strings.TrimSpace(out) == "" {
		t.Errorf("measure code=%d out=%q", code, out)
	}
}
