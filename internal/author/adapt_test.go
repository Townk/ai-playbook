package author

import (
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/store"
)

// TestAdaptPrompt_NamesTargetAndMandatesValidPlaybook verifies the adapt prompt
// names the new target directory and mandates a full, valid-playbook output.
func TestAdaptPrompt_NamesTargetAndMandatesValidPlaybook(t *testing.T) {
	meta := store.Meta{
		Slug:        "build-app",
		Name:        "Build the app",
		Description: "Compile and verify the Android app",
		Workdir:     "~/old/project",
	}
	const target = "/Users/me/code/newproj"
	p := AdaptPrompt(meta, target)

	if !strings.Contains(p, target) {
		t.Errorf("AdaptPrompt must name the new target directory %q; got:\n%s", target, p)
	}
	// It must mandate a VALID playbook: an H1 title AND a runnable block.
	low := strings.ToLower(p)
	if !strings.Contains(low, "h1 title") {
		t.Errorf("AdaptPrompt must mandate an H1 title; got:\n%s", p)
	}
	if !strings.Contains(low, "runnable") || !strings.Contains(low, "block") {
		t.Errorf("AdaptPrompt must mandate at least one runnable block; got:\n%s", p)
	}
	// It must mandate reproducing the full document (not pointing at the original).
	if !strings.Contains(strings.ToUpper(p), "REPRODUCE THE FULL DOCUMENT") {
		t.Errorf("AdaptPrompt must mandate reproducing the full document; got:\n%s", p)
	}
	// The playbook name should be carried into the prompt for context.
	if !strings.Contains(p, "Build the app") {
		t.Errorf("AdaptPrompt should carry the playbook name; got:\n%s", p)
	}
}

// TestAdaptPrompt_EmptyTargetFallback verifies an empty target dir degrades to a
// readable placeholder rather than an empty hole.
func TestAdaptPrompt_EmptyTargetFallback(t *testing.T) {
	p := AdaptPrompt(store.Meta{Slug: "x"}, "")
	if !strings.Contains(p, "current directory") {
		t.Errorf("empty target should fall back to a 'current directory' placeholder; got:\n%s", p)
	}
}
