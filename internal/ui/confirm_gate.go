package ui

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/internal/input"
	"github.com/Townk/ai-playbook/pkg/driver"
	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
)

// groupSizes returns the per-dialog variable counts for n variables: ceil(n/5)
// balanced groups, each ≤5, filled at size ceil(n/groups). n<=0 → nil.
// e.g. 6→[3,3], 13→[5,5,3], 12→[4,4,4].
func groupSizes(n int) []int {
	if n <= 0 {
		return nil
	}
	groups := (n + 4) / 5             // ceil(n/5)
	size := (n + groups - 1) / groups // ceil(n/groups)
	var sizes []int
	for n > 0 {
		s := size
		if s > n {
			s = n
		}
		sizes = append(sizes, s)
		n -= s
	}
	return sizes
}

// confirmVar is one variable shown in the confirmation gate.
type confirmVar struct {
	Name  string
	Value string
	Why   string
}

// buildConfirmVars builds the gate's variable list from the declared front-matter env,
// the heuristic project root, and a getenv func (injected for tests). Each var defaults
// to its declared front-matter `value:` (shown literally — e.g. "$PROJECT_ROOT/data" —
// so it tracks any PROJECT_ROOT override when the shell expands it at export time); a
// non-empty live shell value overrides that default; PROJECT_ROOT always takes the
// heuristic project root. Sorted by name for stable dialog ordering.
func buildConfirmVars(env map[string]frontmatter.EnvValue, projectRoot string, getenv func(string) string) []confirmVar {
	out := make([]confirmVar, 0, len(env))
	for name, ev := range env {
		val := ev.Value // declared front-matter default
		if shell := getenv(name); shell != "" {
			val = shell // an explicit live shell value overrides the default
		}
		if name == "PROJECT_ROOT" {
			val = projectRoot
		}
		out = append(out, confirmVar{Name: name, Value: val, Why: ev.Why})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// gateExportTimeout bounds the synchronous shell export of the confirmed values.
const gateExportTimeout = 10 * time.Second

// confirmGate holds the state of an in-progress pre-run variable confirmation.
type confirmGate struct {
	groups        [][]confirmVar    // balanced ≤5 groups
	values        map[string]string // name → final value (seeded from current, edited by Customize)
	gi            int               // current group index (confirm phase)
	ci            int               // current var index within the group (customize phase)
	customizing   bool              // editing the current group's vars
	block         Button            // the deferred block to run after the gate
	assistedStart bool              // when true, completion enters the assisted ready state (enterAssistedReady) instead of running block
}

// gateAnswerMsg is produced by the gate's askCompletion (via handleAskKey) when one
// confirm/customize overlay resolves; the Update arm feeds it to advanceGate.
type gateAnswerMsg struct {
	value     string
	submitted bool
}

// orchDriver returns the live run driver, or nil when none is attached (tests /
// render-only / async-startup before the orchestrator lands).
func (m model) orchDriver() *driver.Driver {
	if m.orch != nil {
		return m.orch.Drv
	}
	return nil
}

// runOrGate runs block b directly, or — on the first run of an env-bearing playbook —
// opens the confirmation gate (which runs b after the user confirms). When the block
// runs immediately (gate satisfied / no env), it marks the block running before
// returning; the gated path defers that mark to runGateBlock (after the user confirms).
func (m model) runOrGate(b Button) (model, tea.Cmd) {
	if !m.gateSatisfied && len(m.confirmEnv) > 0 {
		return m.beginGate(b)
	}
	m = m.markRunning(b.BlockID)
	return m, m.emitAction(b)
}

// beginGate is the reusable entry point: if the playbook declares env vars and the
// gate is unsatisfied, it raises the first confirm dialog and defers the block;
// otherwise it marks satisfied and runs the block directly. Callable from the
// first-block trigger (Task 7) and, later, the assisted-run start. The variable list
// is built from the model's confirmEnv/projectRoot and the live shell env.
func (m model) beginGate(block Button) (model, tea.Cmd) {
	vars := buildConfirmVars(m.confirmEnv, m.projectRoot, os.Getenv)
	if len(vars) == 0 || m.gateSatisfied {
		m.gateSatisfied = true
		// No gate needed: run the block directly (still threaded through the
		// export-then-run sequence so the returned cmd is non-nil and the path is
		// symmetric with the confirmed-finish path; with no vars the export is a no-op).
		return m.runGateBlock(block, nil)
	}
	g := &confirmGate{values: map[string]string{}, block: block}
	for _, v := range vars {
		g.values[v.Name] = v.Value
	}
	// Partition the vars into balanced ≤5 groups, one confirm dialog each.
	i := 0
	for _, sz := range groupSizes(len(vars)) {
		g.groups = append(g.groups, vars[i:i+sz])
		i += sz
	}
	m.gate = g
	return m.raiseGroupConfirm()
}

// beginAssistedGate is the assisted-run counterpart to beginGate: it raises
// the same confirm-groups flow over the playbook's declared env vars, but
// defers NO block — completion enters the assisted ready state
// (enterAssistedReady) instead of running a specific block. Called once, at
// the start of the guided walk (maybeStartAssisted), so declared vars are
// confirmed before the ready cursor/footer ever appear — not on the first
// [Run] press (that's beginGate/runOrGate's job for the default pager path).
func (m model) beginAssistedGate() (model, tea.Cmd) {
	vars := buildConfirmVars(m.confirmEnv, m.projectRoot, os.Getenv)
	if len(vars) == 0 || m.gateSatisfied {
		m.gateSatisfied = true
		return m.startAssisted(), nil // nothing to confirm → straight to the ready footer
	}
	g := &confirmGate{values: map[string]string{}, assistedStart: true}
	for _, v := range vars {
		g.values[v.Name] = v.Value
	}
	// Partition the vars into balanced ≤5 groups, one confirm dialog each.
	i := 0
	for _, sz := range groupSizes(len(vars)) {
		g.groups = append(g.groups, vars[i:i+sz])
		i += sz
	}
	m.gate = g
	return m.raiseGroupConfirm()
}

// raiseGroupConfirm opens the confirm dialog for the current group (Confirm /
// Customize), routing its result back through askCompletion as a gateAnswerMsg.
func (m model) raiseGroupConfirm() (model, tea.Cmd) {
	g := m.gate
	names := make([]string, 0, len(g.groups[g.gi]))
	for _, v := range g.groups[g.gi] {
		names = append(names, v.Name)
	}
	var b strings.Builder
	b.WriteString("Confirm these variables for this run:\n\n")
	b.WriteString(formatConfirmVars(names, g.values, input.AskInnerWidth()))
	m.ask = input.NewAsk("Variables", b.String(), "", "confirm", nil, "Confirm", "Customize").WithTertiaryButton("Quit")
	m.askMode = true
	m.askCompletion = func(value string, submitted bool) tea.Msg {
		return gateAnswerMsg{value: value, submitted: submitted}
	}
	return m, m.ask.Init()
}

// formatConfirmVars renders names/values as an aligned two-column block: labels
// ("NAME:") are right-padded to a common column (the widest name + ": "), and
// each value is word-wrapped to the remaining width, with continuation lines
// indented to the value column. A single token longer than the available width
// is hard-broken (char-split) rather than left to overflow. Pure/deterministic —
// no dependency beyond lipgloss (for display-width) and strings.
func formatConfirmVars(names []string, values map[string]string, innerW int) string {
	nameW := 0
	for _, n := range names {
		if w := lipgloss.Width(n); w > nameW {
			nameW = w
		}
	}
	valueCol := nameW + 2 // name + ":" + one space
	avail := innerW - valueCol
	if avail < 8 {
		avail = 8
	}
	var lines []string
	for _, n := range names {
		label := n + ":"
		if pad := valueCol - lipgloss.Width(label); pad > 0 {
			label += strings.Repeat(" ", pad)
		}
		for i, chunk := range wrapWithHardBreak(values[n], avail) {
			if i == 0 {
				lines = append(lines, label+chunk)
			} else {
				lines = append(lines, strings.Repeat(" ", valueCol)+chunk)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// wrapWithHardBreak word-wraps s to width (display columns via lipgloss.Width),
// breaking on spaces; a single word wider than width is char-broken across
// multiple chunks so no returned line ever exceeds width. Always returns at
// least one (possibly empty) chunk.
func wrapWithHardBreak(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	for _, word := range words {
		for lipgloss.Width(word) > width {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			var chunk string
			chunk, word = hardBreakToken(word, width)
			lines = append(lines, chunk)
		}
		if word == "" {
			continue
		}
		candidate := word
		if cur != "" {
			candidate = cur + " " + word
		}
		if lipgloss.Width(candidate) <= width {
			cur = candidate
		} else {
			if cur != "" {
				lines = append(lines, cur)
			}
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// hardBreakToken splits token at the largest rune-aligned prefix whose display
// width is ≤ width, returning that prefix and the remainder. Always consumes at
// least one rune so callers make progress even when a single rune is wider than
// width.
func hardBreakToken(token string, width int) (chunk, rest string) {
	runes := []rune(token)
	i := 1
	for i < len(runes) && lipgloss.Width(string(runes[:i+1])) <= width {
		i++
	}
	return string(runes[:i]), string(runes[i:])
}

// raiseVarEdit opens a prefilled single-line dialog for the current customize var.
func (m model) raiseVarEdit() (model, tea.Cmd) {
	g := m.gate
	v := g.groups[g.gi][g.ci]
	prompt := v.Name
	if v.Why != "" {
		prompt += " — " + v.Why
	}
	m.ask = input.NewAsk("Customize", prompt, g.values[v.Name], "line", nil, "", "")
	m.askMode = true
	m.askCompletion = func(value string, submitted bool) tea.Msg {
		return gateAnswerMsg{value: value, submitted: submitted}
	}
	return m, m.ask.Init()
}

// advanceGate consumes one dialog answer and drives the state machine: ESC during a
// per-var edit backs out to the group's confirm dialog (gate intact); ESC on the confirm
// dialog, or choosing Quit, ends the run (gate cleared, tea.Quit); Customize enters the
// per-var edit phase; Confirm (or finishing a group's edits) advances to the next group
// or finishes.
func (m model) advanceGate(value string, submitted bool) (model, tea.Cmd) {
	g := m.gate
	if g == nil {
		return m, nil
	}
	m.askMode = false
	m.ask = nil
	m.askCompletion = nil
	if !submitted {
		if g.customizing { // ESC during a per-var edit → back to the group's confirm
			g.customizing = false
			g.ci = 0
			return m.raiseGroupConfirm()
		}
		m.gate = nil // ESC on the confirm dialog → quit the run
		return m, tea.Quit
	}
	if g.customizing {
		g.values[g.groups[g.gi][g.ci].Name] = value
		g.ci++
		if g.ci < len(g.groups[g.gi]) {
			return m.raiseVarEdit()
		}
		g.customizing = false
		g.ci = 0
		g.gi++
		return m.afterGroup()
	}
	// confirm phase
	if value == "quit" { // Quit button → quit the run
		m.gate = nil
		return m, tea.Quit
	}
	if value == "no" { // Customize → edit this group's vars
		g.customizing = true
		g.ci = 0
		return m.raiseVarEdit()
	}
	g.gi++ // Confirm → next group
	return m.afterGroup()
}

// afterGroup advances to the next group's confirm, or finishes the gate: for
// an assisted-start gate (assistedStart), enters the assisted ready state
// (enterAssistedReady); otherwise exports the final values, marks satisfied,
// and runs the deferred block (runGateBlock) — the default/manual pager path.
func (m model) afterGroup() (model, tea.Cmd) {
	g := m.gate
	if g.gi < len(g.groups) {
		return m.raiseGroupConfirm()
	}
	values, assisted, block := g.values, g.assistedStart, g.block
	m.gate = nil
	if assisted {
		return m.enterAssistedReady(values)
	}
	return m.runGateBlock(block, values)
}

// enterAssistedReady finishes an assisted-start gate: it exports the
// confirmed values via the SAME driver path runGateBlock uses (so the
// exported vars persist into the shell's MAIN context identically), then —
// only once that synchronous export completes — emits assistedStartMsg so
// the ready cursor/footer appear. tea.Sequence guarantees the ordering.
func (m model) enterAssistedReady(values map[string]string) (model, tea.Cmd) {
	m.gateSatisfied = true
	exportCmd := buildExportCmd(values)
	drv := m.orchDriver()
	return m, tea.Sequence(
		func() tea.Msg {
			if drv != nil && exportCmd != "" {
				drv.RunMain(exportCmd, gateExportTimeout)
			}
			return nil
		},
		func() tea.Msg { return assistedStartMsg{} },
	)
}

// runGateBlock marks the gate satisfied, marks the block running, (re-)starts the
// spinner tick, and returns a sequential cmd that exports the confirmed values into the
// shell's MAIN context (so they persist) and THEN runs the deferred block.
// tea.Sequence guarantees the synchronous export completes before the block — which
// reads the exported vars — runs. The returned cmd is non-nil even when there are no
// vars / no orchestrator (tests). The block is marked running HERE (not at the
// call-site) so an ESC-cancelled gate never leaves a block stuck in the running state.
func (m model) runGateBlock(block Button, values map[string]string) (model, tea.Cmd) {
	m.gateSatisfied = true
	m = m.markRunning(block.BlockID)
	exportCmd := buildExportCmd(values)
	drv := m.orchDriver()
	tickCmd := m.startTick() // (re-)arm the spinner; may be nil if loop is already live
	return m, tea.Batch(tickCmd, tea.Sequence(
		func() tea.Msg {
			if drv != nil && exportCmd != "" {
				drv.RunMain(exportCmd, gateExportTimeout)
			}
			return nil
		},
		m.emitAction(block),
	))
}

// expandConfirmedVars resolves references to other confirmed vars inside each value
// (e.g. DATA_DIR="$PROJECT_ROOT/data" → the resolved PROJECT_ROOT path). Only exact
// confirmed-var names are expanded ($NAME / ${NAME}); a literal $ before anything else
// (e.g. "p$ssw0rd") is left untouched so the subsequent single-quote keeps it verbatim.
// Bounded fixed-point iteration resolves transitive references; a cyclic reference is
// left partially expanded rather than looping forever.
func expandConfirmedVars(values map[string]string) map[string]string {
	res := make(map[string]string, len(values))
	pats := make(map[string]*regexp.Regexp, len(values))
	names := make([]string, 0, len(values))
	for n, v := range values {
		res[n] = v
		names = append(names, n)
		pats[n] = regexp.MustCompile(`\$` + regexp.QuoteMeta(n) + `\b`)
	}
	for pass := 0; pass <= len(values); pass++ {
		changed := false
		for _, target := range names {
			s := res[target]
			for _, ref := range names {
				val := res[ref]
				s = strings.ReplaceAll(s, "${"+ref+"}", val)
				// ReplaceAllStringFunc (not ReplaceAllString) so a $ in val is never
				// misread as a $1 group reference.
				s = pats[ref].ReplaceAllStringFunc(s, func(string) string { return val })
			}
			if s != res[target] {
				res[target] = s
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return res
}

// buildExportCmd shell-quotes the final values into a single export command, sorted by
// name for a stable, testable string. Confirmed-var references in derived values are
// expanded first (so "$PROJECT_ROOT/data" exports the resolved path, not a literal). Empty
// values → "".
func buildExportCmd(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	values = expandConfirmedVars(values)
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "export %s=%s; ", n, shellQuote(values[n]))
	}
	return b.String()
}

// shellQuote wraps s in single quotes for POSIX shells, replacing each embedded
// single quote with the close-quote / escaped-quote / reopen-quote sequence, so the
// exported value is injection-safe.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
