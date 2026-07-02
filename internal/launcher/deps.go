// deps.go — pure dependency-graph analysis for `depends_on`. It contains no
// store coupling: the caller supplies a load function that resolves a slug to
// a depNode, so this file can be exercised entirely with an in-memory fake
// (see deps_test.go) and later wired to the real store by another task.
package launcher

import (
	"sort"
	"strings"

	"github.com/Townk/ai-playbook/internal/frontmatter"
)

// depNode is one playbook resolved for dependency analysis: enough to render
// and run it (Body, Cwd) plus its front matter (for further DependsOn edges).
type depNode struct {
	Slug string
	FM   frontmatter.FrontMatter
	Body string
	Cwd  string
}

// DepIssue is one structural problem found while walking a depends_on graph.
// Kind is "dangling" (an edge points at a slug that failed to load; Slug
// holds it) or "cycle" (a depends_on cycle; Path holds its participants in
// discovery order).
type DepIssue struct {
	Kind string
	Slug string
	Path []string
}

// analyzeDeps walks the depends_on graph reachable from rootDeps (the root
// playbook's own DependsOn list — the root itself is never in the result)
// via load, and returns:
//   - order: every distinct dependency in post-order (a dependency appears
//     before anything that needs it), so running order is simply order[0],
//     order[1], ... in sequence;
//   - issues: every distinct dangling slug and every distinct cycle found,
//     deduped the same way internal/validate.detectCycles dedupes — see that
//     function's doc comment for the rationale mirrored here.
//
// The DFS is the same 3-color (unvisited/inStack/done) walk as detectCycles,
// adapted to the slug graph: a node's out-edges are its loaded FM.DependsOn,
// a back-edge (a neighbor still inStack) is a cycle extracted from the DFS
// stack, and a load error is treated as a dangling leaf — the slug is
// recorded as an issue once and marked done so it is never re-attempted or
// re-reported.
func analyzeDeps(rootDeps []string, load func(slug string) (depNode, error)) (order []depNode, issues []DepIssue) {
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := map[string]int{}
	nodes := map[string]depNode{}
	var stack []string

	seenCycles := map[string]bool{}
	seenDangling := map[string]bool{}

	var dfs func(slug string)
	dfs = func(slug string) {
		state[slug] = inStack
		stack = append(stack, slug)

		node, err := load(slug)
		if err != nil {
			if !seenDangling[slug] {
				seenDangling[slug] = true
				issues = append(issues, DepIssue{Kind: "dangling", Slug: slug})
			}
			stack = stack[:len(stack)-1]
			state[slug] = done
			return
		}
		nodes[slug] = node

		for _, next := range node.FM.DependsOn {
			switch state[next] {
			case unvisited:
				dfs(next)
			case inStack:
				// Found a back-edge: extract the cycle path from the stack.
				cyclePath := cyclePathFrom(stack, next)

				key := cycleKey(cyclePath)
				if !seenCycles[key] {
					seenCycles[key] = true
					issues = append(issues, DepIssue{Kind: "cycle", Path: cyclePath})
				}
			case done:
				// already fully explored, no cycle through here
			}
		}

		stack = stack[:len(stack)-1]
		state[slug] = done
		order = append(order, node)
	}

	// Deterministic iteration order over rootDeps (as given) so results are
	// stable across runs for the same input.
	visitedRoot := map[string]bool{}
	for _, slug := range rootDeps {
		if visitedRoot[slug] {
			continue
		}
		visitedRoot[slug] = true
		if state[slug] == unvisited {
			dfs(slug)
		}
	}

	return order, issues
}

// cyclePathFrom extracts the cycle path from the DFS stack when a back-edge
// to back is found: the stack slice starting at back's position, followed by
// back itself (closing the loop). Mirrors the inline extraction in
// internal/validate.detectCycles.
func cyclePathFrom(stack []string, back string) []string {
	start := 0
	for i, s := range stack {
		if s == back {
			start = i
			break
		}
	}
	cyclePath := append([]string{}, stack[start:]...)
	cyclePath = append(cyclePath, back)
	return cyclePath
}

// cycleKey builds a dedup key from the sorted, unique set of slugs in a cycle
// path so the same cycle discovered from different entry points (or via a
// different starting node) is only reported once. Mirrors
// internal/validate.cycleKey.
func cycleKey(path []string) string {
	set := map[string]bool{}
	for _, id := range path {
		set[id] = true
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}
