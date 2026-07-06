// deps.go — pure dependency-graph analysis for `depends_on`. It contains no
// store coupling: the caller supplies a load function that resolves a slug to
// a depNode, so this file can be exercised entirely with an in-memory fake
// (see deps_test.go) and later wired to the real store by another task.
package launcher

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Townk/ai-playbook/internal/autorun"
	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
)

// depNode is one playbook resolved for dependency analysis: enough to render
// and run it (Body, Cwd) plus its front matter (for further DependsOn edges).
// Path/Raw carry the source file's location and raw content when the loader
// read a real file (loadParent does; they feed the run journal's identity) —
// pure in-memory loaders (tests) may leave them empty.
type depNode struct {
	Slug string
	FM   frontmatter.FrontMatter
	Body string
	Cwd  string
	Path string // the source .md path ("" when not file-backed)
	Raw  string // the file's raw content, front matter included
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
//     deduped the same way pkg/playbook/validate.detectCycles dedupes — see that
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
// pkg/playbook/validate.detectCycles.
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
// pkg/playbook/validate.cycleKey.
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

// loadDepNode is the store-backed depNode loader for analyzeDeps: it resolves
// slug through storePathForFn (existence + path, no parse), reads the file,
// and parses its front matter for the full depNode (FM + Body + Cwd). An
// unknown slug, an unreadable file, or unparsable front matter is returned as
// an error naming slug — analyzeDeps records that as a "dangling" issue.
func loadDepNode(slug string) (depNode, error) {
	path, ok := storePathForFn(slug)
	if !ok {
		return depNode{}, fmt.Errorf("dependency %q not found in the store", slug)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return depNode{}, fmt.Errorf("dependency %q: %w", slug, err)
	}
	fm, body, parsed := frontmatter.Parse(string(data))
	if !parsed {
		return depNode{}, fmt.Errorf("dependency %q: front matter failed to parse", slug)
	}
	cwd := ""
	if fm.ProjectBound {
		cwd = resolveProjectRoot(fm.ProjectRoot)
	}
	return depNode{Slug: slug, FM: fm, Body: body, Cwd: cwd}, nil
}

// resolveChain resolves the whole depends_on graph reachable from rootDeps
// (the root/parent playbook's own DependsOn list) through the real store, via
// analyzeDeps + loadDepNode.
func resolveChain(rootDeps []string) (order []depNode, issues []DepIssue) {
	return analyzeDeps(rootDeps, loadDepNode)
}

// runDeps runs nodes headless, in order, aborting on the first non-zero exit
// (later dependencies — and, by the caller not proceeding, the parent — never
// run). Every dependency's RunConfig has SuppressUndeclaredWarning set so only
// the chain's single union warning (see unionDeclared) is ever printed, never
// a per-dependency one. Returns 0 when every node succeeds, else the failing
// node's exit code.
func runDeps(nodes []depNode, overrides map[string]string, autoRollback bool, shell string, out io.Writer) int {
	for _, node := range nodes {
		fmt.Fprintf(out, "\n→ dependency: %s\n", node.Slug)
		// PROJECT_ROOT is passed as DATA (RunConfig.Env), not a process-wide
		// os.Setenv: a save/restore around each node would work too, but every
		// node here has a DIFFERENT root, so mutating the shared process env — even
		// carefully restored — briefly makes one node's root visible to whatever
		// else reads os.Environ() concurrently, and a bug in the restore (or a
		// panic mid-loop) leaves it leaked for good. Scoping it to THIS RunConfig
		// means a later non-bound node's env, and the interactive parent's own
		// driver, never inherit a previous node's root.
		var env map[string]string
		if node.FM.ProjectBound {
			env = map[string]string{"PROJECT_ROOT": node.Cwd}
		}
		code := autorunRunFn(autorun.RunConfig{
			Blocks:                    blocksFor(node.Body),
			EnvVars:                   node.FM.Env,
			EnvOverrides:              overrides,
			Env:                       env,
			Cwd:                       node.Cwd,
			Shell:                     shell,
			Slug:                      node.Slug,
			AutoRollback:              autoRollback,
			SuppressUndeclaredWarning: true,
			Out:                       out,
		})
		if code != 0 {
			return code
		}
	}
	return 0
}

// unionDeclared builds the union of parentFM's declared env vars and every
// dependency's declared env vars — the set the chain's single WarnUndeclared
// call checks --with-env overrides against, so a key only a dependency
// declares is never flagged as undeclared.
func unionDeclared(parentFM frontmatter.FrontMatter, deps []depNode) map[string]frontmatter.EnvValue {
	union := make(map[string]frontmatter.EnvValue, len(parentFM.Env))
	for name, ev := range parentFM.Env {
		union[name] = ev
	}
	for _, dep := range deps {
		for name, ev := range dep.FM.Env {
			union[name] = ev
		}
	}
	return union
}

// printDepIssues prints one line per structural depends_on problem: a
// dangling dependency names the missing slug; a cycle lists its participants
// joined by " → ". Callers exit 2 after printing (issues are always fatal —
// nothing in the chain runs).
func printDepIssues(w io.Writer, issues []DepIssue) {
	for _, is := range issues {
		switch is.Kind {
		case "dangling":
			fmt.Fprintf(w, "ai-playbook: dependency %q not found in the store\n", is.Slug)
		case "cycle":
			fmt.Fprintf(w, "ai-playbook: depends_on cycle: %s\n", strings.Join(is.Path, " → "))
		}
	}
}
