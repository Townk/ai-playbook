package ui

import (
	"strings"
	"testing"
)

func TestReplaceBlockBody(t *testing.T) {
	md := "intro\n\n```diff {id=one}\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n```\n\n```diff {id=two}\n--- a/y\n+++ b/y\n@@ -1 +1 @@\n-c\n+d\n```\n\ntail\n"
	out, ok := replaceBlockBody(md, "two", "--- a/y\n+++ b/y\n@@ -1 +1 @@\n-c\n+D2\n")
	if !ok {
		t.Fatal("block two not found")
	}
	if !strings.Contains(out, "+D2") {
		t.Fatal("body not replaced")
	}
	if !strings.Contains(out, "+b") {
		t.Fatal("block one must be untouched")
	}
	if !strings.Contains(out, "```diff {id=two}") || !strings.Contains(out, "intro") || !strings.Contains(out, "tail\n") {
		t.Fatal("fence line / surrounding text must survive")
	}
	if _, ok := replaceBlockBody(md, "missing", "x"); ok {
		t.Fatal("a missing id must return ok=false")
	}
}

// TestReplaceBlockBodyFirstBlock verifies replacing the FIRST block leaves the second intact.
func TestReplaceBlockBodyFirstBlock(t *testing.T) {
	md := "intro\n\n```diff {id=one}\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n```\n\n```diff {id=two}\n--- a/y\n+++ b/y\n@@ -1 +1 @@\n-c\n+d\n```\n\ntail\n"
	out, ok := replaceBlockBody(md, "one", "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+Z\n")
	if !ok {
		t.Fatal("block one not found")
	}
	if !strings.Contains(out, "+Z") {
		t.Fatal("body not replaced")
	}
	if !strings.Contains(out, "+d") {
		t.Fatal("block two must be untouched")
	}
	if !strings.Contains(out, "```diff {id=one}") {
		t.Fatal("opening fence line of block one must survive")
	}
}

// TestReplaceBlockBodyPrefixNoFalseMatch verifies id "x" does not match a block tagged {id=x2}.
func TestReplaceBlockBodyPrefixNoFalseMatch(t *testing.T) {
	md := "```diff {id=x2}\n-old\n+new\n```\n"
	_, ok := replaceBlockBody(md, "x", "something\n")
	if ok {
		t.Fatal("id 'x' must not match block tagged {id=x2}")
	}
}

// TestReplaceBlockBodyTagWithAttrs verifies the {id=X ...} form (id followed by a space + more attrs).
func TestReplaceBlockBodyTagWithAttrs(t *testing.T) {
	md := "```diff {id=abc class=foo}\n-old\n+new\n```\n"
	out, ok := replaceBlockBody(md, "abc", "-replaced\n+line\n")
	if !ok {
		t.Fatal("block with extra attrs not found")
	}
	if !strings.Contains(out, "-replaced") {
		t.Fatal("body not replaced")
	}
	if !strings.Contains(out, "```diff {id=abc class=foo}") {
		t.Fatal("opening fence with attrs must survive")
	}
}

// TestReplaceBlockBodyMissingClosingFence verifies that a malformed block (no closing fence) returns ok=false.
func TestReplaceBlockBodyMissingClosingFence(t *testing.T) {
	md := "```diff {id=broken}\n-no closing fence\n"
	_, ok := replaceBlockBody(md, "broken", "something\n")
	if ok {
		t.Fatal("missing closing fence must return ok=false")
	}
}
