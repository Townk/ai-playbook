package validate

import (
	"testing"

	"github.com/Townk/ai-playbook/internal/frontmatter"
)

func fm(name, desc, cat, created string) frontmatter.FrontMatter {
	return frontmatter.FrontMatter{Name: name, Description: desc, Category: cat, Created: created}
}
func has(fs []Finding, check string, sev Severity) bool {
	for _, f := range fs {
		if f.Check == check && f.Severity == sev {
			return true
		}
	}
	return false
}

func TestCheck_FrontMatterRequiredKeys(t *testing.T) {
	// missing front matter entirely
	if fs := Check("", frontmatter.FrontMatter{}, false, nil); !has(fs, "front-matter", Error) {
		t.Fatal("!fmOK must yield a front-matter error")
	}
	// present but missing "created"
	fs := Check("", fm("N", "D", "C", ""), true, []Block{{ID: "a", Type: "shell"}})
	if !has(fs, "front-matter", Error) {
		t.Fatal("empty required key must be an error")
	}
	// all keys present → no front-matter error
	fs = Check("", fm("N", "D", "C", "2026-01-01"), true, []Block{{ID: "a", Type: "shell", Lang: "bash"}})
	if has(fs, "front-matter", Error) {
		t.Fatalf("complete front matter must not error: %+v", fs)
	}
}

func TestCheck_DanglingNeeds(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash"}, {ID: "b", Type: "shell", Lang: "bash", Needs: []string{"nope"}}}
	fs := Check("", fm("N", "D", "C", "x"), true, blocks)
	if !has(fs, "needs", Error) {
		t.Fatal("dangling needs= must error")
	}
}

func TestCheck_DuplicateId(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash"}, {ID: "a", Type: "shell", Lang: "bash"}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks); !has(fs, "duplicate-id", Error) {
		t.Fatal("duplicate id must error")
	}
}

func TestCheck_Cycle(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash", Needs: []string{"b"}}, {ID: "b", Type: "shell", Lang: "bash", Needs: []string{"a"}}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks); !has(fs, "cycle", Error) {
		t.Fatal("a→b→a must be a cycle error")
	}
}

func TestCheck_Warnings(t *testing.T) {
	// all static → no-runnable warning; a missing lang → lang warning
	blocks := []Block{{ID: "a", Type: "static", Static: true}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks); !has(fs, "runnable", Warning) {
		t.Fatal("all-static must warn no-runnable")
	}
	blocks = []Block{{ID: "a", Type: "shell", Lang: ""}}
	fs := Check("", fm("N", "D", "C", "x"), true, blocks)
	if !has(fs, "lang", Warning) {
		t.Fatal("missing lang must warn")
	}
	if HasError(fs) {
		t.Fatal("warnings-only must not report HasError")
	}
}

func TestCheck_Clean(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash"}, {ID: "b", Type: "shell", Lang: "bash", Needs: []string{"a"}}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks); len(fs) != 0 {
		t.Fatalf("clean playbook must have no findings; got %+v", fs)
	}
}

func TestCheck_FenceBalance(t *testing.T) {
	ok := fm("N", "D", "C", "x")
	// balanced → no fence finding
	balanced := "# T\n\n```bash\ntrue\n```\n"
	if fs := Check(balanced, ok, true, []Block{{ID: "a", Type: "shell", Lang: "bash"}}); has(fs, "fence", Error) {
		t.Fatalf("balanced fences must not error: %+v", fs)
	}
	// unclosed → fence error
	unclosed := "# T\n\n```bash\ntrue\n"
	if fs := Check(unclosed, ok, true, nil); !has(fs, "fence", Error) {
		t.Fatal("an unclosed ``` fence must error")
	}
	// tilde fence, closed by a longer run → balanced
	tilde := "~~~\nx\n~~~\n"
	if fs := Check(tilde, ok, true, nil); has(fs, "fence", Error) {
		t.Fatalf("balanced tilde fence must not error: %+v", fs)
	}
}
