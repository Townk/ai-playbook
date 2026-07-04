// Package validate is a pure, deterministic leaf: it inspects a parsed
// playbook (front matter + blocks) and reports structural problems as
// Findings. It performs no I/O and imports nothing beyond the frontmatter
// package (for the FrontMatter type) and the Go standard library, so it can
// be driven from tests, the CLI launcher, or any future caller without
// pulling in rendering or terminal concerns.
//
// Public API; pre-1.0, minor versions may still reshape it — see ADR-0009.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
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
	Check    string // "front-matter"|"duplicate-id"|"needs"|"from"|"cycle"|"fence"|"runnable"|"lang"
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
	From   string // id of the block whose retained stdout feeds this one's stdin (from=<id>, ADR-0010); "" if none
}

// Check runs every deterministic check and returns findings (nil ⇔
// structurally clean). rawBody is the front-matter-stripped markdown (Task
// 2's fence scan uses it); fmOK is frontmatter.Parse's ok result.
// bodyLineOffset is the number of lines consumed by front matter (and any
// separator) before rawBody begins in the original file — the fence scan
// adds it to the 1-based line it finds within rawBody so a reported line
// number is FILE-relative, not body-relative. Callers with no front matter
// pass 0.
func Check(rawBody string, fm frontmatter.FrontMatter, fmOK bool, blocks []Block, bodyLineOffset int) []Finding {
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

	// idSet/typeOf are used by the needs-existence check, from= validation,
	// and cycle detection.
	idSet := map[string]bool{}
	typeOf := map[string]string{}
	for _, b := range blocks {
		idSet[b.ID] = true
		typeOf[b.ID] = b.Type
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

	// 4. from= validation (ADR-0010): existence, self-reference, the
	// single-id constraint, and the shell/run-only restriction on both ends
	// (a producer must have output to retain; a consumer must have a stdin
	// to wire the capture into).
	findings = append(findings, fromFindings(blocks, idSet, typeOf)...)

	// 5. needs=/from= cycle detection: DFS over id -> [needs ∪ from that
	// exist in idSet] — the COMBINED graph (from= implies needs=, ADR-0010).
	findings = append(findings, detectCycles(blocks, idSet)...)

	// 6. runnable (Warning)
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

	// 7. lang (Warning)
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
	findings = append(findings, fenceFindings(rawBody, bodyLineOffset)...)

	return findings
}

// fenceFindings scans rawBody line-by-line for an unbalanced code fence
// (``` or ~~~). The UI renderer's normalizeFences silently repairs malformed
// closers, so this check is net-new: it reports (does not repair) a fence
// that is opened but never closed by EOF. See internal/ui/render.go's
// normalizeFences/openFence for the same fence-tracking pattern.
// bodyLineOffset shifts the reported line from body-relative to
// file-relative (see Check's doc comment).
func fenceFindings(rawBody string, bodyLineOffset int) []Finding {
	lines := strings.Split(rawBody, "\n")

	inFence := false
	var fenceChar byte
	var fenceLen int
	var openLine int

	for i, line := range lines {
		lineNo := i + 1
		if !inFence {
			if ch, n, ok := fenceOpen(line); ok {
				inFence = true
				fenceChar = ch
				fenceLen = n
				openLine = lineNo
			}
			continue
		}
		if fenceCloses(line, fenceChar, fenceLen) {
			inFence = false
		}
	}

	if inFence {
		fileLine := openLine + bodyLineOffset
		return []Finding{{
			Severity: Error,
			Check:    "fence",
			Message:  fmt.Sprintf("unclosed code fence opened at line %d", fileLine),
			Where:    fmt.Sprintf("line %d", fileLine),
		}}
	}
	return nil
}

// fenceOpen reports whether line opens a code fence: after up to 3 leading
// spaces, a run of >=3 identical fence chars (backtick or tilde). An info
// string (e.g. "bash {id=a}") may follow the run.
func fenceOpen(line string) (ch byte, n int, ok bool) {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return 0, 0, false
	}
	ch = line[i]
	start := i
	for i < len(line) && line[i] == ch {
		i++
	}
	n = i - start
	if n < 3 {
		return 0, 0, false
	}
	return ch, n, true
}

// fenceCloses reports whether line closes a fence opened with fenceChar/
// fenceLen: after up to 3 leading spaces, the line must be ONLY a run of
// fenceChar of length >= fenceLen (optionally followed by trailing spaces),
// with no other info string.
func fenceCloses(line string, fenceChar byte, fenceLen int) bool {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	start := i
	for i < len(line) && line[i] == fenceChar {
		i++
	}
	runLen := i - start
	if runLen < fenceLen {
		return false
	}
	return i == len(strings.TrimRight(line, " "))
}

// runnableType reports whether t is a block type that produces retainable
// stdout / consumes stdin — the from= restriction (ADR-0010): static has no
// output, and diff/create targets are rejected in v1.
func runnableType(t string) bool {
	return t == "shell" || t == "run"
}

// fromFindings validates every block's from= attribute (ADR-0010): the
// target must exist and not be the block itself, a from= value must name
// exactly one id (a comma list is rejected), the target must be a
// shell/run block (a producer must have output to retain), and the
// declaring block itself must be shell/run (only a runnable block has a
// stdin to wire the capture into).
func fromFindings(blocks []Block, idSet map[string]bool, typeOf map[string]string) []Finding {
	var findings []Finding
	for _, b := range blocks {
		if b.From == "" {
			continue
		}
		if strings.Contains(b.From, ",") {
			findings = append(findings, Finding{
				Severity: Error,
				Check:    "from",
				Message:  fmt.Sprintf("block %q from=%q must reference exactly one id (comma-separated lists are not supported)", b.ID, b.From),
				Where:    b.ID,
			})
			continue
		}
		if !runnableType(b.Type) {
			findings = append(findings, Finding{
				Severity: Error,
				Check:    "from",
				Message:  fmt.Sprintf("block %q is a %s block and cannot declare from= (only shell/run blocks may consume piped output)", b.ID, b.Type),
				Where:    b.ID,
			})
		}
		if b.From == b.ID {
			findings = append(findings, Finding{
				Severity: Error,
				Check:    "from",
				Message:  fmt.Sprintf("block %q cannot pipe from itself", b.ID),
				Where:    b.ID,
			})
			continue
		}
		targetType, exists := typeOf[b.From]
		if !exists {
			findings = append(findings, Finding{
				Severity: Error,
				Check:    "from",
				Message:  fmt.Sprintf("block %q pipes from %q, which does not exist", b.ID, b.From),
				Where:    b.ID,
			})
			continue
		}
		if !runnableType(targetType) {
			findings = append(findings, Finding{
				Severity: Error,
				Check:    "from",
				Message:  fmt.Sprintf("block %q pipes from %q, which is a %s block (only shell/run blocks produce output)", b.ID, b.From, targetType),
				Where:    b.ID,
			})
		}
	}
	return findings
}

// detectCycles runs a DFS over the COMBINED needs= ∪ from= graph (id ->
// needs plus from, when they exist in idSet — from= implies needs= per
// ADR-0010) and reports one Finding per distinct cycle found, deduped by the
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
		if b.From != "" && b.From != b.ID && idSet[b.From] {
			adj[b.ID] = append(adj[b.ID], b.From)
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
						Message:  fmt.Sprintf("needs=/from= cycle: %s", strings.Join(cyclePath, " → ")),
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
