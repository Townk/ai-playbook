package draft

import (
	"strings"
	"testing"
)

func TestValidate_OK(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "bash", Code: "echo a", ID: "fix"},
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
