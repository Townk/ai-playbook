package launcher

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/autorun"
	"github.com/Townk/ai-playbook/internal/frontmatter"
)

// fakeLoader builds a depNode loader from an in-memory slug -> depends_on
// graph; an unknown slug reports a "dangling" load error, mirroring how a
// store-backed loader would fail to find a playbook.
func fakeLoader(graph map[string][]string) func(string) (depNode, error) {
	return func(slug string) (depNode, error) {
		deps, ok := graph[slug]
		if !ok {
			return depNode{}, fmt.Errorf("no playbook for slug %q", slug)
		}
		return depNode{Slug: slug, FM: frontmatter.FrontMatter{DependsOn: deps}}, nil
	}
}

func slugs(nodes []depNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Slug
	}
	return out
}

func TestAnalyzeDeps_LinearOrder(t *testing.T) {
	// parent → a → b ; run order must be b, a
	g := map[string][]string{"a": {"b"}, "b": {}}
	order, issues := analyzeDeps([]string{"a"}, fakeLoader(g))
	if len(issues) != 0 {
		t.Fatalf("issues: %v", issues)
	}
	if got := slugs(order); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Fatalf("order = %v, want [b a]", got)
	}
}

func TestAnalyzeDeps_DiamondDedup(t *testing.T) {
	// a→b, a→c, b→d, c→d : d appears once, before b and c; a last
	g := map[string][]string{"a": {"b", "c"}, "b": {"d"}, "c": {"d"}, "d": {}}
	order, issues := analyzeDeps([]string{"a"}, fakeLoader(g))
	if len(issues) != 0 {
		t.Fatalf("issues: %v", issues)
	}
	got := slugs(order)
	if len(got) != 4 {
		t.Fatalf("want 4 unique nodes, got %v", got)
	}
	pos := map[string]int{}
	for i, s := range got {
		pos[s] = i
	}
	if pos["d"] >= pos["b"] || pos["d"] >= pos["c"] || pos["b"] >= pos["a"] || pos["c"] >= pos["a"] {
		t.Fatalf("bad topo order: %v", got)
	}
}

func TestAnalyzeDeps_Cycle(t *testing.T) {
	g := map[string][]string{"a": {"b"}, "b": {"a"}}
	_, issues := analyzeDeps([]string{"a"}, fakeLoader(g))
	var cycles int
	for _, is := range issues {
		if is.Kind == "cycle" {
			cycles++
		}
	}
	if cycles != 1 {
		t.Fatalf("want exactly 1 cycle issue, got %v", issues)
	}
}

func TestAnalyzeDeps_Dangling(t *testing.T) {
	g := map[string][]string{"a": {"ghost"}}
	_, issues := analyzeDeps([]string{"a"}, fakeLoader(g))
	var dangling int
	for _, is := range issues {
		if is.Kind == "dangling" && is.Slug == "ghost" {
			dangling++
		}
	}
	if dangling != 1 {
		t.Fatalf("want dangling ghost, got %v", issues)
	}
}

// TestAnalyzeDeps_MultipleIssues verifies analyzeDeps collects ALL distinct
// issues in one pass, not just the first — a cycle elsewhere in the graph
// plus a dangling dependency reachable from a different root must both be
// reported.
func TestAnalyzeDeps_MultipleIssues(t *testing.T) {
	g := map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {"b"}, // cycle: b <-> c
		"d": {"ghost1"},
		"e": {"ghost2"},
	}
	_, issues := analyzeDeps([]string{"a", "d", "e"}, fakeLoader(g))

	var cycles, dangling int
	danglingSlugs := map[string]bool{}
	for _, is := range issues {
		switch is.Kind {
		case "cycle":
			cycles++
		case "dangling":
			dangling++
			danglingSlugs[is.Slug] = true
		}
	}
	if cycles != 1 {
		t.Fatalf("want exactly 1 cycle issue, got %d (%v)", cycles, issues)
	}
	if dangling != 2 || !danglingSlugs["ghost1"] || !danglingSlugs["ghost2"] {
		t.Fatalf("want dangling ghost1 and ghost2, got %v", issues)
	}
}

// ---- store-backed wiring: loadDepNode / resolveChain / runDeps / unionDeclared / printDepIssues ----

// writeDepPlaybook writes a minimal playbook fixture under dir named
// name+".md", with extraFrontMatter (e.g. "depends_on:\n  - b\n") spliced into
// its front-matter block, and returns its path. Shared by deps_test.go and
// runcmd_test.go (same package).
func writeDepPlaybook(t *testing.T, dir, name, extraFrontMatter string) string {
	t.Helper()
	path := filepath.Join(dir, name+".md")
	content := "---\nname: " + name + "\n" + extraFrontMatter + "---\n# " + name +
		"\n\n```bash {id=x}\ntrue\n```\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDepNode_StoreBacked(t *testing.T) {
	dir := t.TempDir()
	path := writeDepPlaybook(t, dir, "b", "depends_on:\n  - c\nenv:\n  FOO:\n    value: bar\n    why: test\n")
	defer swap(&storePathForFn, func(slug string) (string, bool) {
		if slug == "b" {
			return path, true
		}
		return "", false
	})()

	node, err := loadDepNode("b")
	if err != nil {
		t.Fatalf("loadDepNode: %v", err)
	}
	if node.Slug != "b" {
		t.Errorf("Slug = %q, want %q", node.Slug, "b")
	}
	if len(node.FM.DependsOn) != 1 || node.FM.DependsOn[0] != "c" {
		t.Errorf("DependsOn = %v, want [c]", node.FM.DependsOn)
	}
	if node.FM.Env["FOO"].Value != "bar" {
		t.Errorf("Env[FOO] = %+v, want value bar", node.FM.Env["FOO"])
	}
}

func TestLoadDepNode_UnknownSlug_Errors(t *testing.T) {
	defer swap(&storePathForFn, func(string) (string, bool) { return "", false })()
	if _, err := loadDepNode("ghost"); err == nil {
		t.Error("unknown slug must error (analyzeDeps records it as dangling)")
	}
}

func TestResolveChain_DanglingViaStore(t *testing.T) {
	defer swap(&storePathForFn, func(string) (string, bool) { return "", false })()
	_, issues := resolveChain([]string{"ghost"})
	if len(issues) != 1 || issues[0].Kind != "dangling" || issues[0].Slug != "ghost" {
		t.Fatalf("issues = %v, want one dangling(ghost)", issues)
	}
}

// TestRunDeps_OrderAndAbort verifies runDeps runs nodes in order, suppresses
// every chain run's own undeclared-override warning, and aborts on the first
// non-zero exit (a later dependency is never invoked; its code is returned).
func TestRunDeps_OrderAndAbort(t *testing.T) {
	nodes := []depNode{
		{Slug: "b", FM: frontmatter.FrontMatter{}, Body: "# b\n"},
		{Slug: "a", FM: frontmatter.FrontMatter{}, Body: "# a\n"},
	}
	var order []string
	restore := swap(&autorunRunFn, func(rc autorun.RunConfig) int {
		order = append(order, rc.Slug)
		if !rc.SuppressUndeclaredWarning {
			t.Errorf("%s: SuppressUndeclaredWarning = false, want true", rc.Slug)
		}
		return 0
	})
	defer restore()

	var buf bytes.Buffer
	if code := runDeps(nodes, nil, true, "", &buf); code != 0 {
		t.Fatalf("runDeps = %d, want 0", code)
	}
	if want := []string{"b", "a"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}

	// Abort: b fails → a is NEVER invoked; runDeps returns b's exit code.
	order = nil
	autorunRunFn = func(rc autorun.RunConfig) int {
		order = append(order, rc.Slug)
		if rc.Slug == "b" {
			return 7
		}
		return 0
	}
	if code := runDeps(nodes, nil, true, "", &buf); code != 7 {
		t.Fatalf("runDeps abort = %d, want 7", code)
	}
	if want := []string{"b"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("abort order = %v, want %v (a never invoked)", order, want)
	}
}

func TestUnionDeclared(t *testing.T) {
	parentFM := frontmatter.FrontMatter{Env: map[string]frontmatter.EnvValue{"P": {Value: "1"}}}
	deps := []depNode{
		{Slug: "a", FM: frontmatter.FrontMatter{Env: map[string]frontmatter.EnvValue{"A": {Value: "2"}}}},
	}
	union := unionDeclared(parentFM, deps)
	if _, ok := union["P"]; !ok {
		t.Error("union missing the parent's declared var P")
	}
	if _, ok := union["A"]; !ok {
		t.Error("union missing the dependency's declared var A")
	}
}

func TestPrintDepIssues(t *testing.T) {
	var buf bytes.Buffer
	printDepIssues(&buf, []DepIssue{
		{Kind: "dangling", Slug: "ghost"},
		{Kind: "cycle", Path: []string{"a", "b", "a"}},
	})
	out := buf.String()
	if !strings.Contains(out, `dependency "ghost" not found in the store`) {
		t.Errorf("missing dangling line:\n%s", out)
	}
	if !strings.Contains(out, "depends_on cycle: a → b → a") {
		t.Errorf("missing cycle line:\n%s", out)
	}
}
