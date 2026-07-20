package launcher

// helpers_coverage_test.go — behavioral tests for the small pure helpers the
// integration entry points (SessionMain/Assist/AnswerMain) use; the entry
// points themselves are exercised live, not unit-tested (see docs/BACKLOG.md
// Ideas: E2E tests for the integration entry points).

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestCachedTime covers the RFC3339 badge parse: a valid stamp yields (t,true);
// empty and malformed values yield (zero,false) so no badge is shown.
func TestCachedTime(t *testing.T) {
	ts, ok := cachedTime("2026-07-19T12:00:00Z")
	if !ok || !ts.Equal(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("valid stamp = (%v,%v)", ts, ok)
	}
	if _, ok := cachedTime(""); ok {
		t.Error("empty stamp must not parse")
	}
	if _, ok := cachedTime("yesterday-ish"); ok {
		t.Error("malformed stamp must not parse")
	}
}

// TestStoreDirsAndPathFor covers the config→store wiring: the resolved Dirs
// carry non-empty global/project directories, and an unknown slug routes to
// (,"",false) through storePathFor.
func TestStoreDirsAndPathFor(t *testing.T) {
	d, err := storeDirs()
	if err != nil {
		t.Fatalf("storeDirs: %v", err)
	}
	if d.Global == "" {
		t.Error("global store dir must resolve")
	}
	if _, ok := storePathFor("definitely-not-a-saved-playbook-slug"); ok {
		t.Error("an unknown slug must not resolve to a path")
	}
}

// TestAISkipNote pins that the degrade note names the configured backend.
func TestAISkipNote(t *testing.T) {
	note := aiSkipNote("claude")
	if !strings.Contains(note, "claude") || !strings.Contains(note, "AI review skipped") {
		t.Errorf("skip note must name the backend: %q", note)
	}
}

// TestIsNoBackend covers the missing-backend classifier both ways.
func TestIsNoBackend(t *testing.T) {
	for _, e := range []string{
		"exec: \"claude\": executable file not found in $PATH",
		"harness \"foo\" not yet supported",
		"no backend available",
	} {
		if !isNoBackend(errors.New(e)) {
			t.Errorf("%q must classify as no-backend", e)
		}
	}
	if isNoBackend(nil) {
		t.Error("nil must not classify")
	}
	if isNoBackend(errors.New("model returned malformed JSON")) {
		t.Error("an ordinary failure must not classify as no-backend")
	}
}
