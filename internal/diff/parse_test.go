package diff

import (
	"reflect"
	"testing"
)

func TestParse_SingleHunk(t *testing.T) {
	patch := "--- a/foo.go\n+++ b/foo.go\n@@ -1,3 +1,3 @@\n ctx\n-old line\n+new line\n more\n"
	got := Parse(patch)
	want := []FileDiff{{OldPath: "a/foo.go", NewPath: "b/foo.go", Hunks: []Hunk{{Lines: []Line{
		{OpContext, "ctx"}, {OpDel, "old line"}, {OpAdd, "new line"}, {OpContext, "more"},
	}}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse =\n%#v\nwant\n%#v", got, want)
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
