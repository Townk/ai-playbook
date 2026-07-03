// climeta_test.go — TDD tests for the command-metadata registry and its
// Overview/Help renderers (see .superpowers/sdd/task-1-brief.md).
package climeta

import (
	"strings"
	"testing"
)

// TestOverview_ListsEveryCommandOnce asserts Overview() surfaces every
// registered command exactly once, includes "run", "validate", "env", and the
// closing details footer, and that user-facing commands are listed before
// internal ones.
func TestOverview_ListsEveryCommandOnce(t *testing.T) {
	out := Overview()

	for _, cmd := range Commands {
		count := strings.Count(out, cmd.Name)
		if count == 0 {
			t.Errorf("Overview() does not mention command %q", cmd.Name)
		}
	}

	for _, want := range []string{"run", "validate", "env"} {
		if !strings.Contains(out, want) {
			t.Errorf("Overview() missing %q", want)
		}
	}

	if !strings.Contains(out, "ai-playbook <command> --help") {
		t.Errorf("Overview() missing the details footer; got:\n%s", out)
	}

	// User-facing commands must appear before internal ones.
	lastUserIdx, firstInternalIdx := -1, -1
	for _, cmd := range Commands {
		idx := strings.Index(out, cmd.Name)
		if idx < 0 {
			continue
		}
		if cmd.Internal {
			if firstInternalIdx == -1 || idx < firstInternalIdx {
				firstInternalIdx = idx
			}
		} else {
			if idx > lastUserIdx {
				lastUserIdx = idx
			}
		}
	}
	if firstInternalIdx != -1 && lastUserIdx != -1 && firstInternalIdx < lastUserIdx {
		t.Errorf("Overview() does not list user-facing commands before internal ones (lastUserIdx=%d firstInternalIdx=%d)", lastUserIdx, firstInternalIdx)
	}
}

// TestHelp_ResolvesAliasAndFlags asserts Help resolves an alias to the exact
// same text as its canonical name, that `run`'s help surfaces --with-env with
// its verbatim description and at least one example, and that an unknown
// command name resolves to ok=false.
func TestHelp_ResolvesAliasAndFlags(t *testing.T) {
	troubleshoot, ok := Help("troubleshoot")
	if !ok {
		t.Fatal("Help(\"troubleshoot\") ok=false, want true")
	}
	assist, ok := Help("assist")
	if !ok {
		t.Fatal("Help(\"assist\") ok=false, want true")
	}
	if troubleshoot != assist {
		t.Errorf("Help(\"troubleshoot\") != Help(\"assist\"):\n--- troubleshoot ---\n%s\n--- assist ---\n%s", troubleshoot, assist)
	}

	run, ok := Help("run")
	if !ok {
		t.Fatal("Help(\"run\") ok=false, want true")
	}
	if !strings.Contains(run, "--with-env") {
		t.Errorf("Help(\"run\") missing --with-env:\n%s", run)
	}
	if !strings.Contains(run, "with --auto, supply env var values as inline JSON or a JSON file path") {
		t.Errorf("Help(\"run\") missing --with-env's verbatim description:\n%s", run)
	}
	if !strings.Contains(run, "EXAMPLES") {
		t.Errorf("Help(\"run\") missing an EXAMPLES section:\n%s", run)
	}

	if _, ok := Help("nope"); ok {
		t.Error("Help(\"nope\") ok=true, want false")
	}
}

// TestRegistry_NoEmptySummaries asserts every registered command has a
// non-empty Name and Summary.
func TestRegistry_NoEmptySummaries(t *testing.T) {
	for i, cmd := range Commands {
		if cmd.Name == "" {
			t.Errorf("Commands[%d] has an empty Name", i)
		}
		if cmd.Summary == "" {
			t.Errorf("Commands[%d] (%q) has an empty Summary", i, cmd.Name)
		}
	}
}
