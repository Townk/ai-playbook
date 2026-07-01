package diff

import (
	"strings"
	"testing"
)

// ch.05's exact case: the file has `timeout = 99`; the patch was authored against
// `timeout = 30` and proposes `timeout = 60`, anchored on [server]/host/port and
// max_connections context. ConflictMarkup must locate the hunk and splice a block.
const ch05File = "[server]\n" +
	"host = localhost\n" +
	"port = 8080\n" +
	"timeout = 99\n" +
	"max_connections = 100\n" +
	"\n" +
	"[logging]\n" +
	"level = info\n" +
	"output = stdout\n"

const ch05Patch = "--- a/projects/drifted/settings.conf\n" +
	"+++ b/projects/drifted/settings.conf\n" +
	"@@ -2,7 +2,7 @@\n" +
	" host = localhost\n" +
	" port = 8080\n" +
	"-timeout = 30\n" +
	"+timeout = 60\n" +
	" max_connections = 100\n" +
	" \n" +
	" [logging]\n"

func TestConflictMarkup_Ch05Case(t *testing.T) {
	marked, ok := ConflictMarkup(ch05File, Parse(ch05Patch))
	if !ok {
		t.Fatal("ConflictMarkup must locate the ch.05 hunk (ok=true)")
	}

	for _, want := range []string{
		markerCurrent,
		"timeout = 99",
		markerExpected,
		"timeout = 30",
		markerProposed,
		"timeout = 60",
	} {
		if !strings.Contains(marked, want) {
			t.Fatalf("marked output missing %q:\n%s", want, marked)
		}
	}

	// Surrounding lines + the 3-way block must appear in order:
	// [server] … -[current]- 99 -[expected]- 30 -[proposed]- 60 … max_connections … [logging]
	iServer := strings.Index(marked, "[server]")
	iCurrent := strings.Index(marked, markerCurrent)
	i99 := strings.Index(marked, "timeout = 99")
	iExpected := strings.Index(marked, markerExpected)
	i30 := strings.Index(marked, "timeout = 30")
	iProposed := strings.Index(marked, markerProposed)
	i60 := strings.Index(marked, "timeout = 60")
	iMax := strings.Index(marked, "max_connections = 100")
	iLog := strings.Index(marked, "[logging]")
	order := []int{iServer, iCurrent, i99, iExpected, i30, iProposed, i60, iMax, iLog}
	for i := 1; i < len(order); i++ {
		if order[i-1] < 0 || order[i] < 0 || order[i-1] >= order[i] {
			t.Fatalf("lines out of order (%v):\n%s", order, marked)
		}
	}
}

// A patch whose context cannot be found anywhere in the file is unlocatable → ok=false.
func TestConflictMarkup_UnlocatableReturnsFalse(t *testing.T) {
	patch := "--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n aaa\n-bbb\n+ccc\n ddd\n"
	if marked, ok := ConflictMarkup("nothing matches here\nat all\n", Parse(patch)); ok {
		t.Fatalf("unlocatable patch must return ok=false, got marked:\n%s", marked)
	}
}

// HasConflictMarkers: true while an opener survives, false once the user deletes them.
func TestHasConflictMarkers(t *testing.T) {
	marked, ok := ConflictMarkup(ch05File, Parse(ch05Patch))
	if !ok {
		t.Fatal("precondition: markup must succeed")
	}
	if !HasConflictMarkers(marked) {
		t.Fatal("a freshly marked file must report unresolved conflict markers")
	}

	resolved := "[server]\nhost = localhost\nport = 8080\ntimeout = 60\nmax_connections = 100\n\n[logging]\n"
	if HasConflictMarkers(resolved) {
		t.Fatal("a file with the openers deleted must NOT report conflict markers")
	}

	// A stray plain dashed rule must not count as unresolved.
	if HasConflictMarkers("some line\n" + strings.Repeat("-", fenceWidth) + "\nmore\n") {
		t.Fatal("a plain ---- rule must not be treated as an unresolved marker")
	}
}
