package diff

import (
	"reflect"
	"testing"
)

func TestParse_SingleHunk(t *testing.T) {
	patch := "--- a/foo.go\n+++ b/foo.go\n@@ -1,3 +1,3 @@\n ctx\n-old line\n+new line\n more\n"
	got := Parse(patch)
	want := []FileDiff{{OldPath: "a/foo.go", NewPath: "b/foo.go", Hunks: []Hunk{{OldStart: 1, NewStart: 1, Lines: []Line{
		{OpContext, "ctx"}, {OpDel, "old line"}, {OpAdd, "new line"}, {OpContext, "more"},
	}}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse =\n%#v\nwant\n%#v", got, want)
	}
}

func TestParse_HunkStartNumbers(t *testing.T) {
	// The @@ START numbers must be captured (COUNTS still ignored).
	patch := "--- a/x\n+++ b/x\n@@ -10,3 +12,4 @@\n ctx\n-old\n+new\n"
	h := Parse(patch)[0].Hunks[0]
	if h.OldStart != 10 || h.NewStart != 12 {
		t.Fatalf("hunk starts = %d/%d, want 10/12", h.OldStart, h.NewStart)
	}
}

func TestParse_HunkStartNoCounts(t *testing.T) {
	// Counts omitted (`@@ -1 +1 @@`) → starts still parse, defaulting to those.
	patch := "--- a/x\n+++ b/x\n@@ -5 +7 @@\n-a\n+b\n"
	h := Parse(patch)[0].Hunks[0]
	if h.OldStart != 5 || h.NewStart != 7 {
		t.Fatalf("hunk starts = %d/%d, want 5/7", h.OldStart, h.NewStart)
	}
}

func TestParse_HunkStartMalformed(t *testing.T) {
	// A malformed @@ header leaves the starts at zero (no panic, no truncation).
	patch := "--- a/x\n+++ b/x\n@@ garbage @@\n-a\n+b\n"
	h := Parse(patch)[0].Hunks[0]
	if h.OldStart != 0 || h.NewStart != 0 {
		t.Fatalf("malformed header starts = %d/%d, want 0/0", h.OldStart, h.NewStart)
	}
	if len(h.Lines) != 2 {
		t.Fatalf("malformed header must not drop body: got %d lines", len(h.Lines))
	}
}

func TestParse_ToleratesMiscountedHeader(t *testing.T) {
	// header says 1,1 but the body has 3 lines — drive off the body, parse all 3.
	patch := "--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n a\n-b\n+c\n"
	h := Parse(patch)[0].Hunks[0]
	if len(h.Lines) != 3 {
		t.Fatalf("miscounted header must not truncate the body: got %d lines", len(h.Lines))
	}
}

func TestParse_MultiFileMultiHunk(t *testing.T) {
	patch := "--- a/one\n+++ b/one\n@@ -1 +1 @@\n-x\n+y\n--- a/two\n+++ b/two\n@@ -1 +1 @@\n-p\n+q\n"
	got := Parse(patch)
	if len(got) != 2 || got[1].NewPath != "b/two" {
		t.Fatalf("multi-file parse wrong: %#v", got)
	}
}

func TestParse_HeaderLookingLinesInsideHunk(t *testing.T) {
	// A6a: a deleted line whose CONTENT starts with "-- " (e.g. an SQL comment)
	// arrives, diff-prefixed, as "--- SQL comment" — indistinguishable by prefix
	// alone from a file header. Inside an open hunk it must be read as a del
	// line, not misparsed as a new file header (which would truncate/misattribute
	// the rest of the hunk). Symmetrically for an added "++ " line → "+++ text".
	patch := "--- a/query.sql\n+++ b/query.sql\n@@ -1,3 +1,3 @@\n context\n" +
		"--- SQL comment\n++ added op\n more\n"
	got := Parse(patch)
	if len(got) != 1 {
		t.Fatalf("expected one file, got %d: %#v", len(got), got)
	}
	f := got[0]
	if f.NewPath != "b/query.sql" {
		t.Fatalf("NewPath clobbered by in-hunk '+++'-looking line: got %q, want %q", f.NewPath, "b/query.sql")
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("expected one hunk, got %d: %#v", len(f.Hunks), f.Hunks)
	}
	want := []Line{
		{OpContext, "context"},
		{OpDel, "-- SQL comment"},
		{OpAdd, "+ added op"},
		{OpContext, "more"},
	}
	if !reflect.DeepEqual(f.Hunks[0].Lines, want) {
		t.Fatalf("Lines =\n%#v\nwant\n%#v", f.Hunks[0].Lines, want)
	}
}

func TestParse_AdjacentDelAddHeaderLookalikes(t *testing.T) {
	// A6a, paired-line hole: a deleted "-- " line IMMEDIATELY followed by an
	// added "++ " line arrives as "--- x" then "+++ y" — the same shape as a
	// file-header pair. A real header pair is always followed by an "@@" hunk
	// header; a body del/add pair is followed by more body lines. The pair
	// below must parse as del+add, not as a phantom mid-hunk file header.
	patch := "--- a/query.sql\n+++ b/query.sql\n@@ -1,3 +1,3 @@\n context\n" +
		"--- old comment\n+++ new comment\n more\n"
	got := Parse(patch)
	if len(got) != 1 {
		t.Fatalf("expected one file, got %d: %#v", len(got), got)
	}
	f := got[0]
	if f.NewPath != "b/query.sql" {
		t.Fatalf("NewPath clobbered by in-hunk del/add header-lookalike pair: got %q, want %q", f.NewPath, "b/query.sql")
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("expected one hunk, got %d: %#v", len(f.Hunks), f.Hunks)
	}
	want := []Line{
		{OpContext, "context"},
		{OpDel, "-- old comment"},
		{OpAdd, "++ new comment"},
		{OpContext, "more"},
	}
	if !reflect.DeepEqual(f.Hunks[0].Lines, want) {
		t.Fatalf("Lines =\n%#v\nwant\n%#v", f.Hunks[0].Lines, want)
	}
}

func TestParse_BareBlankContextLine(t *testing.T) {
	// A blank context line emitted WITHOUT a leading space (bare \n) must be
	// preserved as Line{OpContext, ""} rather than dropped.
	patch := "--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n ctx\n\n more\n"
	got := Parse(patch)
	if len(got) == 0 || len(got[0].Hunks) == 0 {
		t.Fatal("parse returned no hunks")
	}
	lines := got[0].Hunks[0].Lines
	want := []Line{{OpContext, "ctx"}, {OpContext, ""}, {OpContext, "more"}}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("bare blank context line not preserved:\ngot  %#v\nwant %#v", lines, want)
	}
}
