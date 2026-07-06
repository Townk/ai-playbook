package draft

import (
	"strings"
	"testing"
)

func TestValidate_OK(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "bash", Code: "echo a", ID: "fix", Timeout: "15m"},
	}}}, Verify: &Step{Lang: "bash", Code: "ok"}}
	if err := Validate(pb, true); err != nil {
		t.Fatalf("want valid, got %v", err)
	}
}

func TestValidate_Violations(t *testing.T) {
	cases := []struct {
		name string
		pb   Playbook
		req  bool
		want string
	}{
		{"no title", Playbook{Sections: []Section{{Heading: "S", Content: []ContentItem{{Kind: "code", Lang: "bash", Code: "x", ID: "a"}}}}}, false, "title"},
		{"no runnable block", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{{Kind: "code", Lang: "console", Code: "x", Static: true}}}}}, false, "runnable"},
		{"dup id", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "x", ID: "fix"},
			{Kind: "code", Lang: "bash", Code: "y", ID: "fix"},
		}}}}, false, "duplicate id"},
		{"missing verify", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{{Kind: "code", Lang: "bash", Code: "x", ID: "fix"}}}}}, true, "verify"},
		{"bad kind", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{{Kind: "bogus"}, {Kind: "code", Lang: "bash", Code: "x", ID: "a"}}}}}, false, "kind"},
		// Fix 1: empty lang on a code block.
		{"empty lang code", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "", Code: "echo x", ID: "fix"},    // empty lang — should error
			{Kind: "code", Lang: "bash", Code: "echo y", ID: "ok"}, // valid runnable block
		}}}}, false, "lang"},
		// Fix 2: content block id "verify" collides with the top-level verify reservation.
		{"dup id verify", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo ok", ID: "verify"},
		}}}, Verify: &Step{Lang: "bash", Code: "ok"}}, false, "duplicate id"},
		// Finding A7b: needs= must reference a declared id.
		{"dangling needs", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo x", ID: "fix", Needs: []string{"missing"}},
		}}}}, false, "needs"},
		// Finding A7b: rollback= must reference a declared id.
		{"dangling rollback", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo x", ID: "fix", Rollback: "missing"},
		}}}}, false, "rollback"},
		// Finding A7b: a two-block needs= cycle must be rejected.
		{"needs cycle", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo a", ID: "a", Needs: []string{"b"}},
			{Kind: "code", Lang: "bash", Code: "echo b", ID: "b", Needs: []string{"a"}},
		}}}}, false, "cycle"},
		// Finding A7b: an id containing whitespace would mis-split the {id=...} fence tag.
		{"bad id syntax", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo x", ID: "foo bar"},
		}}}}, false, "id"},
		// Finding A7b: a file= value containing "}" would mis-split the {id=...} fence tag.
		{"bad file syntax", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "go", Code: "package main", ID: "new", File: "a b}.txt"},
		}}}}, false, "file"},
		// A backtick in the rendered fence info string is CommonMark-invalid (an
		// info string on a backtick fence may not contain backticks) — a second
		// fence-corruption vector, rejected at submit time like the {}= splitters.
		{"backtick in id", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo x", ID: "fix`x"},
		}}}}, false, "id"},
		{"backtick in file", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "go", Code: "package main", ID: "new", File: "a`b.txt"},
		}}}}, false, "file"},
		// from= (ADR-0010): existence, self-reference, and the
		// producer/consumer shell/run-only restriction, mirroring
		// pkg/playbook/validate's rules at submit time.
		{"from missing target", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "cat", ID: "c", From: "nope"},
		}}}}, false, "from"},
		{"from self reference", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "cat", ID: "a", From: "a"},
		}}}}, false, "from"},
		{"from producer static rejected", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "console", Code: "out", Static: true, ID: "p"},
			{Kind: "code", Lang: "bash", Code: "cat", ID: "c", From: "p"},
		}}}}, false, "from"},
		{"from producer diff rejected", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "diff", Code: "--- a\n+++ b", ID: "p"},
			{Kind: "code", Lang: "bash", Code: "cat", ID: "c", From: "p"},
		}}}}, false, "from"},
		{"from producer create rejected", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "go", Code: "package main", ID: "p", File: "x.go"},
			{Kind: "code", Lang: "bash", Code: "cat", ID: "c", From: "p"},
		}}}}, false, "from"},
		{"from consumer static rejected", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo p", ID: "p"},
			{Kind: "code", Lang: "console", Code: "out", Static: true, ID: "c", From: "p"},
		}}}}, false, "from"},
		{"from consumer diff rejected", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo p", ID: "p"},
			{Kind: "code", Lang: "diff", Code: "--- a\n+++ b", ID: "c", From: "p"},
		}}}}, false, "from"},
		{"from consumer create rejected", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo p", ID: "p"},
			{Kind: "code", Lang: "go", Code: "package main", ID: "c", File: "x.go", From: "p"},
		}}}}, false, "from"},
		// A non-Static block whose lang is non-executable (console/text/…)
		// re-parses as type=static (pkg/playbook.ClassifyType's nonExecLang
		// rule), so it must be rejected as a from= consumer/producer HERE
		// too — otherwise draft accepts what the file validator then fails.
		{"from consumer console implicit static rejected", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo p", ID: "p"},
			{Kind: "code", Lang: "console", Code: "out", ID: "c", From: "p"},
		}}}}, false, "from"},
		{"from producer console implicit static rejected", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "console", Code: "out", ID: "p"},
			{Kind: "code", Lang: "bash", Code: "cat", ID: "c", From: "p"},
		}}}}, false, "from"},
		{"from comma list rejected", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo p", ID: "p"},
			{Kind: "code", Lang: "bash", Code: "echo q", ID: "q"},
			{Kind: "code", Lang: "bash", Code: "cat", ID: "c", From: "p,q"},
		}}}}, false, "from"},
		// Combined needs=/from= cycle: a's from= edge to b plus b's
		// needs= edge to a forms a cycle over the COMBINED graph.
		{"combined needs from cycle", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo a", ID: "a", From: "b"},
			{Kind: "code", Lang: "bash", Code: "echo b", ID: "b", Needs: []string{"a"}},
		}}}}, false, "cycle"},
		// timeout= mirrors the file validator's contract (Error) tier at
		// submit time: a declared value must parse as a POSITIVE Go duration.
		{"unparseable timeout", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo x", ID: "fix", Timeout: "banana"},
		}}}}, false, "timeout"},
		{"zero timeout", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo x", ID: "fix", Timeout: "0"},
		}}}}, false, "timeout"},
		{"negative timeout", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "bash", Code: "echo x", ID: "fix", Timeout: "-5s"},
		}}}}, false, "timeout"},
		// The value is malformed regardless of placement — an unparseable
		// timeout on a static item is rejected too (the file validator keeps
		// the unparseable case an Error even on non-runnable blocks).
		{"unparseable timeout on static", Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
			{Kind: "code", Lang: "console", Code: "out", Static: true, Timeout: "banana"},
			{Kind: "code", Lang: "bash", Code: "echo x", ID: "fix"},
		}}}}, false, "timeout"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.pb, c.req)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

// Finding A7b: a valid needs=/rollback= chain (referencing declared ids, no
// cycle) must be accepted.
func TestValidate_NeedsChainAccepted(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "bash", Code: "echo a", ID: "a"},
		{Kind: "code", Lang: "bash", Code: "echo b", ID: "b", Needs: []string{"a"}, Rollback: "a"},
	}}}, Verify: &Step{Lang: "bash", Code: "ok", Needs: []string{"b"}}}
	if err := Validate(pb, true); err != nil {
		t.Fatalf("want a valid needs/rollback chain accepted, got %v", err)
	}
}

// TestValidate_FromChainAccepted verifies a valid shell producer -> run
// consumer from= chain (the flagship python-reads-stdin case, ADR-0010)
// passes validation, including a verify step that itself declares from=.
func TestValidate_FromChainAccepted(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "bash", Code: "echo hi", ID: "produce"},
		{Kind: "code", Lang: "python", Code: "import sys; print(sys.stdin.read())", ID: "consume", From: "produce"},
	}}}, Verify: &Step{Lang: "bash", Code: "true", From: "consume"}}
	if err := Validate(pb, true); err != nil {
		t.Fatalf("want a valid from= chain accepted, got %v", err)
	}
}
