package autorun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StepResult is one executed (or skipped/cancelled) step, for the summary + log.
type StepResult struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Exit    int    `json:"exit"`
	Status  string `json:"status"` // "ok" | "failed" | "skipped" | "rolledback" | "cancelled"
	// TimedOutAfter is the formatted effective ceiling ("1s", "10m") when the
	// step was killed at its timeout; "" for every other outcome. Lets the
	// summary row (and the JSON run log) tell a hang-kill from a real failure.
	TimedOutAfter string `json:"timed_out_after,omitempty"`
	OutputPath    string `json:"output,omitempty"`
}

// Summarize renders a one-line-per-step summary table (human-readable, to stdout).
func Summarize(results []StepResult) string {
	var buf strings.Builder
	for _, r := range results {
		symbol := "–" // default for unknown status
		switch r.Status {
		case StatusOK:
			symbol = "✓"
		case StatusFailed:
			symbol = "✗"
		case StatusRolledBack:
			symbol = "↺"
		case StatusSkipped:
			symbol = "–"
		case StatusCancelled:
			symbol = "⊘"
		}

		line := fmt.Sprintf("  %s %-9s %-7s", symbol, r.Status, r.ID)
		if r.Status == StatusFailed && r.TimedOutAfter != "" {
			// A hang-kill, not a real error: name the effective ceiling
			// (consistent with the per-step "timed out after <d>" output).
			line += fmt.Sprintf(" (timed out after %s, exit %d)", r.TimedOutAfter, r.Exit)
		} else if r.Status == StatusOK && r.Exit != 0 {
			line += fmt.Sprintf(" (exit %d)", r.Exit)
		} else if r.Status == StatusFailed && r.Exit != 0 {
			line += fmt.Sprintf(" (exit %d)", r.Exit)
		} else if (r.Status == StatusOK || r.Status == StatusFailed) && r.Exit == 0 {
			line += fmt.Sprintf(" (exit %d)", r.Exit)
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.String()
}

// WriteRunLog writes results as JSON to <dir>/runs/<stamp>-<slug>.json and returns the
// path. dir is the data-dir root (cache.DefaultRoot()); stamp is a caller-supplied
// timestamp (injected for deterministic tests). Creates <dir>/runs if absent.
func WriteRunLog(dir, stamp, slug string, results []StepResult) (string, error) {
	runsDir := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		return "", err
	}

	path := filepath.Join(runsDir, stamp+"-"+slug+".json")

	envelope := struct {
		Slug  string       `json:"slug"`
		Stamp string       `json:"stamp"`
		Steps []StepResult `json:"steps"`
	}{
		Slug:  slug,
		Stamp: stamp,
		Steps: results,
	}

	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}

	return path, nil
}
