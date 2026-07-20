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

// A6c: hunks are ordered, but indexOfSeq anchors every hunk's leading context
// from the top of the file. With a repeated context block, hunk 2's "pre" also
// matches ABOVE hunk 1's already-located region; the overlap check at
// conflict.go:114 then silently drops hunk 2. A running `from` offset (the end
// of the previous hunk's region) must be threaded through the per-hunk
// searches so hunk 2 anchors on its own (later) occurrence instead.
func TestConflictMarkup_SecondHunkAnchoredAfterFirst(t *testing.T) {
	file := "common\nA\ncommon\nB\ncommon\n"
	patch := "--- a/f\n+++ b/f\n" +
		"@@ -1,3 +1,3 @@\n common\n-A\n+A2\n common\n" +
		"@@ -3,3 +3,3 @@\n common\n-B\n+B2\n common\n"

	marked, ok := ConflictMarkup(file, Parse(patch))
	if !ok {
		t.Fatal("ConflictMarkup must locate at least one hunk")
	}
	if n := strings.Count(marked, markerCurrent); n != 2 {
		t.Fatalf("both hunks must produce a conflict region, got %d marked region(s):\n%s", n, marked)
	}
	for _, want := range []string{"A", "A2", "B", "B2"} {
		if !strings.Contains(marked, want) {
			t.Fatalf("marked output missing %q:\n%s", want, marked)
		}
	}
}

// Guard for the A6b design choice: tab expansion happens at DISPLAY build time
// (Rows/Render), NOT in Parse — ConflictMarkup matches parsed hunk text
// byte-for-byte against the raw file (indexOfSeq) and splices the patch's
// proposed lines into it, so Parse-level expansion would make every
// tab-indented hunk unlocatable and would swap the user's tabs for spaces in
// the spliced [expected]/[proposed] sections. This test pins both: a
// tab-indented file still anchors, and the spliced lines keep their tabs.
func TestConflictMarkup_TabIndentedFilePreservesTabs(t *testing.T) {
	file := "func f() {\n\tif ok {\n\t\told()\n\t}\n}\n"
	patch := "--- a/f.go\n+++ b/f.go\n@@ -2,3 +2,3 @@\n" +
		" \tif ok {\n-\t\tdrifted()\n+\t\tnew()\n \t}\n"

	marked, ok := ConflictMarkup(file, Parse(patch))
	if !ok {
		t.Fatal("a tab-indented hunk must still anchor (tabs must NOT be expanded in Parse)")
	}
	for _, want := range []string{"\t\told()", "\t\tdrifted()", "\t\tnew()"} {
		if !strings.Contains(marked, want) {
			t.Fatalf("marked output must keep tab indentation, missing %q:\n%s", want, marked)
		}
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

// TestLabelLine covers both fence forms: a short label is dash-padded to the
// fixed fence width, and an over-long label still gets a closing "---".
func TestLabelLine(t *testing.T) {
	short := labelLine(markerCurrent)
	if len(short) != fenceWidth || !strings.HasSuffix(short, "-") {
		t.Errorf("short label must pad to fenceWidth: %q (len %d)", short, len(short))
	}
	long := labelLine(strings.Repeat("x", fenceWidth+5))
	if !strings.HasSuffix(long, "---") {
		t.Errorf("over-long label must still close with ---: %q", long)
	}
}
