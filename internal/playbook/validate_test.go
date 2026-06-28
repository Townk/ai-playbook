package playbook

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
