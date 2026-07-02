package launcher

import (
	"fmt"
	"reflect"
	"testing"

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
	if !(pos["d"] < pos["b"] && pos["d"] < pos["c"] && pos["b"] < pos["a"] && pos["c"] < pos["a"]) {
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
