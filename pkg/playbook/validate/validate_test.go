package validate

import (
	"fmt"
	"testing"

	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
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
	if fs := Check("", frontmatter.FrontMatter{}, false, nil, 0); !has(fs, "front-matter", Error) {
		t.Fatal("!fmOK must yield a front-matter error")
	}
	// present but missing "created"
	fs := Check("", fm("N", "D", "C", ""), true, []Block{{ID: "a", Type: "shell"}}, 0)
	if !has(fs, "front-matter", Error) {
		t.Fatal("empty required key must be an error")
	}
	// all keys present → no front-matter error
	fs = Check("", fm("N", "D", "C", "2026-01-01"), true, []Block{{ID: "a", Type: "shell", Lang: "bash"}}, 0)
	if has(fs, "front-matter", Error) {
		t.Fatalf("complete front matter must not error: %+v", fs)
	}
}

func TestCheck_DanglingNeeds(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash"}, {ID: "b", Type: "shell", Lang: "bash", Needs: []string{"nope"}}}
	fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0)
	if !has(fs, "needs", Error) {
		t.Fatal("dangling needs= must error")
	}
}

func TestCheck_DuplicateId(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash"}, {ID: "a", Type: "shell", Lang: "bash"}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0); !has(fs, "duplicate-id", Error) {
		t.Fatal("duplicate id must error")
	}
}

func TestCheck_Cycle(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash", Needs: []string{"b"}}, {ID: "b", Type: "shell", Lang: "bash", Needs: []string{"a"}}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0); !has(fs, "cycle", Error) {
		t.Fatal("a→b→a must be a cycle error")
	}
}

// --- from= (ADR-0010): existence, self-reference, producer/consumer type
// restrictions, single-id constraint, and combined needs=/from= cycles.

func TestCheck_FromMissingTarget(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash", From: "nope"}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0); !has(fs, "from", Error) {
		t.Fatal("from= referencing a nonexistent block must error")
	}
}

func TestCheck_FromSelfReference(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash", From: "a"}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0); !has(fs, "from", Error) {
		t.Fatal("from= referencing the block itself must error")
	}
}

func TestCheck_FromProducerMustBeRunnable(t *testing.T) {
	for _, producerType := range []string{"static", "diff", "create"} {
		t.Run(producerType, func(t *testing.T) {
			blocks := []Block{
				{ID: "p", Type: producerType, Static: producerType == "static"},
				{ID: "c", Type: "shell", Lang: "bash", From: "p"},
			}
			if fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0); !has(fs, "from", Error) {
				t.Fatalf("from= targeting a %s block must error", producerType)
			}
		})
	}
}

func TestCheck_FromConsumerMustBeRunnable(t *testing.T) {
	for _, consumerType := range []string{"static", "diff", "create"} {
		t.Run(consumerType, func(t *testing.T) {
			blocks := []Block{
				{ID: "p", Type: "shell", Lang: "bash"},
				{ID: "c", Type: consumerType, Static: consumerType == "static", From: "p"},
			}
			if fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0); !has(fs, "from", Error) {
				t.Fatalf("a %s block declaring from= must error", consumerType)
			}
		})
	}
}

func TestCheck_FromCommaListRejected(t *testing.T) {
	blocks := []Block{
		{ID: "p", Type: "shell", Lang: "bash"},
		{ID: "q", Type: "shell", Lang: "bash"},
		{ID: "c", Type: "shell", Lang: "bash", From: "p,q"},
	}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0); !has(fs, "from", Error) {
		t.Fatal("a comma-separated from= list must error")
	}
}

// TestCheck_FromCombinedCycle proves cycle detection runs over the COMBINED
// needs= ∪ from= graph: a's from= edge to b plus b's needs= edge to a forms a
// cycle even though neither attribute alone would.
func TestCheck_FromCombinedCycle(t *testing.T) {
	blocks := []Block{
		{ID: "a", Type: "shell", Lang: "bash", From: "b"},
		{ID: "b", Type: "shell", Lang: "bash", Needs: []string{"a"}},
	}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0); !has(fs, "cycle", Error) {
		t.Fatal("a combined needs=/from= cycle must be detected")
	}
}

func TestCheck_FromValid(t *testing.T) {
	blocks := []Block{
		{ID: "p", Type: "shell", Lang: "bash"},
		{ID: "c", Type: "run", Lang: "python", From: "p"},
	}
	fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0)
	if has(fs, "from", Error) || has(fs, "cycle", Error) {
		t.Fatalf("a valid shell->run from= chain must not error: %+v", fs)
	}
}

func TestCheck_Warnings(t *testing.T) {
	// all static → no-runnable warning; a missing lang → lang warning
	blocks := []Block{{ID: "a", Type: "static", Static: true}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0); !has(fs, "runnable", Warning) {
		t.Fatal("all-static must warn no-runnable")
	}
	blocks = []Block{{ID: "a", Type: "shell", Lang: ""}}
	fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0)
	if !has(fs, "lang", Warning) {
		t.Fatal("missing lang must warn")
	}
	if HasError(fs) {
		t.Fatal("warnings-only must not report HasError")
	}
}

func TestCheck_Clean(t *testing.T) {
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash"}, {ID: "b", Type: "shell", Lang: "bash", Needs: []string{"a"}}}
	if fs := Check("", fm("N", "D", "C", "x"), true, blocks, 0); len(fs) != 0 {
		t.Fatalf("clean playbook must have no findings; got %+v", fs)
	}
}

func TestCheck_FenceBalance(t *testing.T) {
	ok := fm("N", "D", "C", "x")
	// balanced → no fence finding
	balanced := "# T\n\n```bash\ntrue\n```\n"
	if fs := Check(balanced, ok, true, []Block{{ID: "a", Type: "shell", Lang: "bash"}}, 0); has(fs, "fence", Error) {
		t.Fatalf("balanced fences must not error: %+v", fs)
	}
	// unclosed → fence error
	unclosed := "# T\n\n```bash\ntrue\n"
	if fs := Check(unclosed, ok, true, nil, 0); !has(fs, "fence", Error) {
		t.Fatal("an unclosed ``` fence must error")
	}
	// tilde fence, closed by a longer run → balanced
	tilde := "~~~\nx\n~~~\n"
	if fs := Check(tilde, ok, true, nil, 0); has(fs, "fence", Error) {
		t.Fatalf("balanced tilde fence must not error: %+v", fs)
	}
}

// TestCheck_FenceBalance_LineOffset proves the fence finding's reported line
// is FILE-relative: bodyLineOffset shifts the body-relative open line (found
// by fenceFindings) by the number of lines the front matter occupied in the
// original file, so an editor jumping to the reported line lands on the
// actual unclosed fence — not on some earlier line inside the body alone.
func TestCheck_FenceBalance_LineOffset(t *testing.T) {
	ok := fm("N", "D", "C", "x")
	// The unclosed fence opens on body-line 2 ("```bash").
	body := "# T\n```bash\ntrue\n"
	const offset = 5 // e.g. a 5-line --- front-matter block preceding body
	fs := Check(body, ok, true, nil, offset)
	if !has(fs, "fence", Error) {
		t.Fatal("an unclosed ``` fence must error")
	}
	const wantLine = 2 + offset // body-line 2 + offset ⇒ file line 7
	wantWhere := fmt.Sprintf("line %d", wantLine)
	wantMsg := fmt.Sprintf("unclosed code fence opened at line %d", wantLine)
	for _, f := range fs {
		if f.Check != "fence" {
			continue
		}
		if f.Where != wantWhere {
			t.Fatalf("fence finding Where = %q, want %q", f.Where, wantWhere)
		}
		if f.Message != wantMsg {
			t.Fatalf("fence finding Message = %q, want %q", f.Message, wantMsg)
		}
	}
}
