// Package validate is a pure, deterministic leaf: it inspects a parsed
// playbook (front matter + blocks) and reports structural problems as
// Findings. It performs no I/O and imports nothing beyond the frontmatter
// package (for the FrontMatter type) and the Go standard library, so it can
// be driven from tests, the CLI launcher, or any future caller without
// pulling in rendering or terminal concerns.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Townk/ai-playbook/internal/frontmatter"
)

// Severity classifies a Finding: Error findings fail validation (drive a
// non-zero exit code via HasError); Warning findings are advisory only.
type Severity int

const (
	Error Severity = iota
	Warning
)

// Finding is one structural problem detected by Check.
type Finding struct {
	Severity Severity
	Check    string // "front-matter"|"duplicate-id"|"needs"|"cycle"|"fence"|"runnable"|"lang"
	Message  string
	Where    string // block id | "line N" | "front matter"
}

// Block is validate's DTO for a playbook code block — the launcher converts
// ui.Block into this (validate never imports ui, keeping this package a pure
// leaf).
type Block struct {
	ID     string
	Type   string // "shell"|"run"|"diff"|"static"|"create"
	Lang   string
	Needs  []string
	Static bool
}

// Check runs every deterministic check and returns findings (nil ⇔
// structurally clean). rawBody is the front-matter-stripped markdown (Task
// 2's fence scan uses it); fmOK is frontmatter.Parse's ok result.
func Check(rawBody string, fm frontmatter.FrontMatter, fmOK bool, blocks []Block) []Finding {
	var findings []Finding

	// 1. front-matter
	if !fmOK {
		findings = append(findings, Finding{
			Severity: Error,
			Check:    "front-matter",
			Message:  "missing or malformed front matter (a playbook needs a leading --- YAML block)",
			Where:    "front matter",
		})
	} else {
		required := []struct{ key, value string }{
			{"name", fm.Name},
			{"description", fm.Description},
			{"category", fm.Category},
			{"created", fm.Created},
		}
		for _, r := range required {
			if strings.TrimSpace(r.value) == "" {
				findings = append(findings, Finding{
					Severity: Error,
					Check:    "front-matter",
					Message:  fmt.Sprintf("missing required key %q", r.key),
					Where:    "front matter",
				})
			}
		}
	}

	// 2. duplicate-id
	count := map[string]int{}
	for _, b := range blocks {
		count[b.ID]++
	}
	reportedDup := map[string]bool{}
	for _, b := range blocks {
		if count[b.ID] > 1 && !reportedDup[b.ID] {
			reportedDup[b.ID] = true
			findings = append(findings, Finding{
				Severity: Error,
				Check:    "duplicate-id",
				Message:  fmt.Sprintf("block id %q is used %d times", b.ID, count[b.ID]),
				Where:    b.ID,
			})
		}
	}

	// idSet is used by both the needs-existence check and cycle detection.
	idSet := map[string]bool{}
	for _, b := range blocks {
		idSet[b.ID] = true
	}

	// 3. needs= existence
	for _, b := range blocks {
		for _, need := range b.Needs {
			if !idSet[need] {
				findings = append(findings, Finding{
					Severity: Error,
					Check:    "needs",
					Message:  fmt.Sprintf("block %q needs %q, which does not exist", b.ID, need),
					Where:    b.ID,
				})
			}
		}
	}

	// 4. needs= cycle detection: DFS over id -> [needs that exist in idSet].
	findings = append(findings, detectCycles(blocks, idSet)...)

	// 5. runnable (Warning)
	runnable := false
	for _, b := range blocks {
		if !b.Static {
			runnable = true
			break
		}
	}
	if !runnable {
		findings = append(findings, Finding{
			Severity: Warning,
			Check:    "runnable",
			Message:  "no runnable blocks — nothing to execute",
			Where:    "",
		})
	}

	// 6. lang (Warning)
	for _, b := range blocks {
		if !b.Static && strings.TrimSpace(b.Lang) == "" {
			findings = append(findings, Finding{
				Severity: Warning,
				Check:    "lang",
				Message:  fmt.Sprintf("block %q has no language", b.ID),
				Where:    b.ID,
			})
		}
	}

	// fence balance: added in Task 2

	return findings
}

// detectCycles runs a DFS over the needs= graph (id -> needs that exist in
// idSet) and reports one Finding per distinct cycle found, deduped by the
// sorted set of ids participating in the cycle so the same cycle is never
// reported more than once (e.g. once per node it could be entered from).
func detectCycles(blocks []Block, idSet map[string]bool) []Finding {
	adj := map[string][]string{}
	for _, b := range blocks {
		for _, need := range b.Needs {
			if idSet[need] {
				adj[b.ID] = append(adj[b.ID], need)
			}
		}
	}

	var findings []Finding
	seenCycles := map[string]bool{}

	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := map[string]int{}
	var stack []string

	var dfs func(id string)
	dfs = func(id string) {
		state[id] = inStack
		stack = append(stack, id)

		for _, next := range adj[id] {
			switch state[next] {
			case unvisited:
				dfs(next)
			case inStack:
				// Found a back-edge: extract the cycle path from the stack.
				start := 0
				for i, s := range stack {
					if s == next {
						start = i
						break
					}
				}
				cyclePath := append([]string{}, stack[start:]...)
				cyclePath = append(cyclePath, next)

				key := cycleKey(cyclePath)
				if !seenCycles[key] {
					seenCycles[key] = true
					findings = append(findings, Finding{
						Severity: Error,
						Check:    "cycle",
						Message:  fmt.Sprintf("needs= cycle: %s", strings.Join(cyclePath, " → ")),
						Where:    "",
					})
				}
			case done:
				// already fully explored, no cycle through here
			}
		}

		stack = stack[:len(stack)-1]
		state[id] = done
	}

	// Deterministic iteration order over blocks (as given) so results are
	// stable across runs for the same input.
	visitedRoot := map[string]bool{}
	for _, b := range blocks {
		if visitedRoot[b.ID] {
			continue
		}
		visitedRoot[b.ID] = true
		if state[b.ID] == unvisited {
			dfs(b.ID)
		}
	}

	return findings
}

// cycleKey builds a dedup key from the sorted, unique set of ids in a cycle
// path so the same cycle discovered from different entry points (or via a
// different starting node) is only reported once.
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

// HasError reports whether any finding is an Error (drives the exit code).
func HasError(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == Error {
			return true
		}
	}
	return false
}
