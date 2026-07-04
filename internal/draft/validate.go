package draft

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// idTokenRe matches characters that would break the {id=… file=… needs=…
// rollback=…} fence tag ParseFenceInfo (blocks.go) expects: it
// splits the tag on whitespace and "=", and the tag itself is delimited by
// "{"/"}". Any of those characters inside an id or a file= value would
// mis-split the tag and silently corrupt the rendered playbook. A backtick is
// rejected too: the tag is rendered into the fence INFO STRING, and CommonMark
// forbids backticks in a backtick fence's info string — one in an id/file=
// value would end the fence early and corrupt the document (a second
// fence-corruption vector besides the payload runs fenceLen already handles).
var idTokenRe = regexp.MustCompile("[\\s{}=`]")

// needsRef captures one runnable code block's identity + cross-references for
// Validate's second pass (needs/rollback existence + cycle detection), which
// needs the full `seen` set of declared ids before it can run. id == ""
// marks a block with no explicit id: Render auto-assigns those a "step-N" id
// (render.go), a name this model call cannot predict, so such a block can be
// a needs/rollback SOURCE but never a valid TARGET.
type needsRef struct {
	id       string
	needs    []string
	rollback string
}

// Validate checks the semantic rules the JSON schema cannot express. It returns
// nil when valid, else one error joining every violation (so a re-submitting
// model sees all problems at once). requireVerify demands a top-level Verify
// (set for a troubleshooting/fix playbook; create passes false).
func Validate(pb Playbook, requireVerify bool) error {
	var errs []string
	if strings.TrimSpace(pb.Title) == "" {
		errs = append(errs, "title is required")
	}
	runnable := 0
	seen := map[string]bool{}
	if pb.Verify != nil {
		seen["verify"] = true
	}

	var refs []needsRef

	for si, sec := range pb.Sections {
		for ci, it := range sec.Content {
			switch it.Kind {
			case "text", "callout":
				// prose: nothing structural to check
			case "code":
				if strings.TrimSpace(it.Lang) == "" {
					errs = append(errs, fmt.Sprintf("section %d content %d: code block requires a lang", si, ci))
				}
				if !it.Static {
					runnable++
					if it.ID != "" {
						if seen[it.ID] {
							errs = append(errs, fmt.Sprintf("duplicate id %q", it.ID))
						}
						seen[it.ID] = true
						if idTokenRe.MatchString(it.ID) {
							errs = append(errs, fmt.Sprintf("id %q must not contain whitespace, \"{\", \"}\", \"=\", or a backtick", it.ID))
						}
					}
					if it.File != "" && idTokenRe.MatchString(it.File) {
						errs = append(errs, fmt.Sprintf("file %q must not contain whitespace, \"{\", \"}\", \"=\", or a backtick", it.File))
					}
					if len(it.Needs) > 0 || it.Rollback != "" {
						refs = append(refs, needsRef{id: it.ID, needs: it.Needs, rollback: it.Rollback})
					}
				}
			default:
				errs = append(errs, fmt.Sprintf("section %d content %d: unknown kind %q (want text|callout|code)", si, ci, it.Kind))
			}
		}
	}
	if pb.Verify != nil {
		if strings.TrimSpace(pb.Verify.Lang) == "" {
			errs = append(errs, "verify command requires a lang")
		}
		if len(pb.Verify.Needs) > 0 {
			refs = append(refs, needsRef{id: "verify", needs: pb.Verify.Needs})
		}
	}
	if runnable == 0 {
		errs = append(errs, "at least one runnable (non-static) code block is required")
	}
	if requireVerify && pb.Verify == nil {
		errs = append(errs, "a top-level verify command is required for this playbook")
	}

	// needs=/rollback= must reference a declared id (verify counts, an
	// auto-assigned "step-N" id does not — see the ref doc comment above).
	for _, r := range refs {
		where := r.id
		if where == "" {
			where = "(auto-assigned id)"
		}
		for _, need := range r.needs {
			if !seen[need] {
				errs = append(errs, fmt.Sprintf("block %q needs %q, which does not exist", where, need))
			}
		}
		if r.rollback != "" && !seen[r.rollback] {
			errs = append(errs, fmt.Sprintf("block %q rollback %q, which does not exist", where, r.rollback))
		}
	}
	errs = append(errs, detectNeedsCycles(refs, seen)...)

	if len(errs) == 0 {
		return nil
	}
	return errors.New("invalid playbook: " + strings.Join(errs, "; "))
}

// detectNeedsCycles runs a DFS over the needs= graph (id -> needs that exist
// in seen) and returns one message per distinct cycle, deduped by the sorted
// set of ids participating in it. Mirrors pkg/playbook/validate's detectCycles
// (same algorithm and message shape) — the two packages don't share code
// (pkg/playbook/validate is a leaf that never imports draft, and vice versa)
// but should behave identically on the same needs= graph.
func detectNeedsCycles(refs []needsRef, seen map[string]bool) []string {
	adj := map[string][]string{}
	var order []string
	for _, r := range refs {
		if r.id == "" {
			continue // auto-assigned ids can't be a needs= target, so can't sit in a cycle
		}
		order = append(order, r.id)
		for _, need := range r.needs {
			if seen[need] {
				adj[r.id] = append(adj[r.id], need)
			}
		}
	}

	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := map[string]int{}
	var stack []string
	var out []string
	seenCycle := map[string]bool{}

	var dfs func(id string)
	dfs = func(id string) {
		state[id] = inStack
		stack = append(stack, id)
		for _, next := range adj[id] {
			switch state[next] {
			case unvisited:
				dfs(next)
			case inStack:
				start := 0
				for i, s := range stack {
					if s == next {
						start = i
						break
					}
				}
				cyclePath := append([]string{}, stack[start:]...)
				cyclePath = append(cyclePath, next)
				key := needsCycleKey(cyclePath)
				if !seenCycle[key] {
					seenCycle[key] = true
					out = append(out, fmt.Sprintf("needs= cycle: %s", strings.Join(cyclePath, " → ")))
				}
			case done:
				// already fully explored, no cycle through here
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = done
	}

	visitedRoot := map[string]bool{}
	for _, id := range order {
		if visitedRoot[id] {
			continue
		}
		visitedRoot[id] = true
		if state[id] == unvisited {
			dfs(id)
		}
	}
	return out
}

// needsCycleKey builds a dedup key from the sorted, unique set of ids in a
// cycle path so the same cycle discovered from different entry points is
// only reported once.
func needsCycleKey(path []string) string {
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
