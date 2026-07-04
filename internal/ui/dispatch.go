package ui

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	idiff "github.com/Townk/ai-playbook/internal/diff"
)

// emitAction performs a button's action. When an in-process orchestrator is wired
// (m.orch != nil) it returns a tea.Cmd that drives the orchestrator directly (off
// the event loop) and feeds a resultMsg back. When there is no orchestrator
// (m.orch == nil — render-only / degraded startup) an orch-driven action is a clean
// NO-OP returning nil; the shell-action buttons are rendered disabled in that state
// (driverPending / canRegenerate gating), so this is the safety floor. The returned
// Cmd is nil when there is nothing to feed back, so callers can unconditionally batch it.
func (m model) emitAction(b Button) tea.Cmd {
	if m.orch != nil {
		return m.orchCmd(b)
	}
	return nil
}

// shellActionsReady reports whether the shell-backed buttons may render enabled and
// dispatch. It is false ONLY during the async-startup window (driverPending), while
// the background orchestrator is still opening; in that window the shell-action
// buttons render dimmed and are inert. Once the orchestrator lands (orchReadyMsg)
// this is true and the buttons render/behave exactly as before. The copy button
// never consults this — it needs no shell.
func (m model) shellActionsReady() bool { return !m.driverPending }

// buttonInert reports whether a button's dispatch is currently a swallowed no-op, so
// it must not receive a hint label (F19) — otherwise a hint letter would select a
// button that does nothing. Three cases: a DRIFTED block's apply-diff (you can't apply a
// patch that no longer matches — the drift region's resolve/regenerate buttons are the
// live paths), any shell-action button during the async-startup window (the
// orchestrator isn't open yet), and a run button on an in-flight from-chain member
// (runOrChain's re-entrancy guard swallows it). Mirrors exactly what the dispatch
// sites swallow.
func (m model) buttonInert(b Button) bool {
	if b.Kind == "apply-diff" && m.blockStates[b.BlockID].Drifted {
		return true
	}
	if isShellActionKind(b.Kind) && !m.shellActionsReady() {
		return true
	}
	// A run button on an in-flight materialization-chain member: the step is
	// already dispatched or queued, so a second activation would be swallowed by
	// runOrChain's re-entrancy guard — don't hand it a hint label.
	if b.Kind == "run" && m.chainMember(b.BlockID) {
		return true
	}
	return false
}

// runningActions maps the four "start a run and spin" button kinds to the
// blockRunState.Action each one sets. They share one arm (Status="running",
// Action, SpinFrame=0, emitAction, reflow, batch startTick+flash+ac), so
// activateButton table-drives them off this lookup instead of four clones.
var runningActions = map[string]string{
	"apply-diff":  "apply",
	"undo-diff":   "undo",
	"create":      "create",
	"undo-create": "undo",
}

// activateButton runs the dispatch for one button — shared by the mouse-click and
// keyboard-hint paths so the two can never drift apart (finding C1a). It sets the
// flash key (BlockID:Kind) and resolves the button's kind to its action.
//
// Callers gate INERTNESS before calling: the mouse path swallows a shell-action
// button during async startup (returns before this, so no flash); the hint path
// never assigns a label to an inert button (buttonInert filters assignHintLabels),
// so it cannot reach here with one. The one inert case handled inline is a DRIFTED
// block's apply-diff (reachable only by mouse) — it flashes then no-ops, exactly
// as the mouse path did before.
//
// The assisted-footer (`assist-`) arm lives here, so hint activation of a footer
// button now dispatches through assistedActivate instead of falling through to
// emitAction → nil and silently doing nothing (finding A3a).
func (m model) activateButton(b Button) (model, tea.Cmd) {
	m.flashKey = b.BlockID + ":" + b.Kind
	switch {
	case b.Kind == "toggle":
		m = m.handleToggle(b.BlockID) // handleToggle already calls reflow
		return m, m.flashCmd()
	case b.Kind == "run":
		var ac tea.Cmd
		m, ac = m.runOrChain(b)
		m.reflow()
		return m, tea.Batch(m.startTick(), m.flashCmd(), ac)
	case b.Kind == "stop":
		m.markStopped(b.BlockID)
		ac := m.emitAction(b)
		m.reflow()
		return m, tea.Batch(m.flashCmd(), ac)
	case b.Kind == "apply-diff" && m.blockStates[b.BlockID].Drifted:
		// Drifted diff block: only apply-diff is inert (you genuinely can't apply a
		// patch that no longer matches). view-diff/diff still open the read-only
		// side-by-side pager below so a drifted diff can be viewed.
		return m, nil
	}

	if action, ok := runningActions[b.Kind]; ok {
		st := m.blockStates[b.BlockID]
		st.Status = "running"
		st.Action = action
		st.SpinFrame = 0
		m.blockStates[b.BlockID] = st
		ac := m.emitAction(b)
		m.reflow()
		return m, tea.Batch(m.startTick(), m.flashCmd(), ac)
	}

	switch {
	case b.Kind == "undo-resolve":
		return m.undoResolve(b)
	case b.Kind == "diff" || b.Kind == "view-diff":
		return m.activateDiffButton(b)
	case b.Kind == "drift-resolve":
		return m.driftResolveDispatch(b)
	case b.Kind == "drift-regen":
		if m.orch == nil {
			return m, nil
		}
		st := m.blockStates[b.BlockID]
		st.Status = "regenerating"
		st.RegenFailed = false
		st.RegenNote = ""
		st.SpinFrame = 0
		m.blockStates[b.BlockID] = st
		m.reflow()
		return m, tea.Batch(m.startTick(), m.flashCmd(), m.driftRegenCmd(b.BlockID, b.Payload))
	case b.Kind == "regenerate":
		m.flashKey = "cached:regenerate"
		// In-process: re-author via the orchestrator and re-arm the parser
		// (REPLACE). Else flash-only (no regenerate path wired).
		if cmd := m.beginRegenerate(); cmd != nil {
			return m, tea.Batch(m.flashCmd(), cmd)
		}
		m.reflow()
		return m, m.flashCmd()
	case b.Kind == "followup":
		if cmd := m.beginFollowupStream(b.BlockID, b.Payload); cmd != nil {
			return m, tea.Batch(m.flashCmd(), cmd)
		}
		m.reflow()
		return m, m.flashCmd()
	case b.Kind == "rollback":
		return m.beginRollback(b.BlockID)
	case b.Kind == "confirm-yes" || b.Kind == "confirm-no":
		if cmd := m.resolveConfirm(b.Kind == "confirm-yes"); cmd != nil {
			return m, tea.Batch(m.flashCmd(), cmd)
		}
		m.reflow()
		return m, m.flashCmd()
	case strings.HasPrefix(b.Kind, "assist-"):
		return m.assistedActivate(b.Kind)
	case b.Kind == "edit":
		return m.editDispatch()
	}

	ac := m.emitAction(b)
	m.reflow()
	return m, tea.Batch(m.flashCmd(), ac)
}

// runOrChain runs the block behind a clicked run button, first materializing any
// of its from= producers that have not completed ok this session (ADR-0010). When
// the block has no unrun producer the chain is just [block] and this reduces to a
// plain runOrGate single run; otherwise the producers (in dependency order) and
// finally the consumer are queued and run sequentially — each an ordinary block
// run with its own status pill/log — the first step through runOrGate (so the
// env-confirm gate still applies) and the rest advanced by handleResult as each
// result lands ok. A step failing stops the chain.
//
// The default pager is the only caller: the assisted (GUIDED) cadence surfaces a
// consumer as a ready step only once its producer has run (NextRunnable folds
// from= into its effective needs), so its Run press never needs a multi-step
// chain — assistedActivate keeps calling runOrGate directly.
func (m model) runOrChain(b Button) (model, tea.Cmd) {
	// Re-entrancy guard 1: while a chain is in flight, every member (the running
	// chainStep + the queued steps, including the clicked consumer itself) is
	// inert — a second click mid-window must not recompute the chain and
	// re-dispatch the still-running producer (its side effects would run twice).
	if m.chainMember(b.BlockID) {
		return m, nil
	}
	chain := m.fromChain(b.BlockID)
	// Re-entrancy guard 2: a chain member is already running — the producer was
	// started standalone (or under another consumer's chain) and hasn't finished,
	// so its capture isn't ready. fromChain sees "running" ≠ "ok" and would
	// include (re-dispatch) it; refuse the click instead — once its result lands
	// the capture serves and a fresh click chains normally.
	for _, id := range chain {
		if m.blockStates[id].Status == "running" {
			return m, nil
		}
	}
	if len(chain) <= 1 {
		return m.runOrGate(b) // no producer to materialize → ordinary single run
	}
	m.chainQueue = chain[1:]
	first := chain[0]
	m.chainStep = first
	return m.runOrGate(Button{Kind: "run", Payload: m.blockCommand(first), BlockID: first})
}

// chainMember reports whether block id belongs to the in-flight materialization
// chain: it is the currently dispatched chainStep or one of the queued steps.
// Chain members are inert to a new run click/hint for the whole chain window.
func (m model) chainMember(id string) bool {
	if id == "" {
		return false
	}
	if id == m.chainStep {
		return true
	}
	for _, q := range m.chainQueue {
		if q == id {
			return true
		}
	}
	return false
}

// fromChain returns the ordered block ids to run so that consumer consumerID
// receives its piped stdin: every transitive from= producer that has NOT
// completed ok this session, in dependency order (producers before consumers),
// followed by consumerID itself. A producer already ok this session is omitted —
// its retained capture serves, so it is never re-run — but the walk still stops
// there (its own upstream is already materialized). Returns just [consumerID]
// when nothing upstream needs materializing. A visited set guards against a cycle
// (validation rejects from= cycles, so this is defensive only).
func (m model) fromChain(consumerID string) []string {
	var order []string
	seen := map[string]bool{}
	var visit func(id string)
	visit = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		if blk, ok := m.blockByID(id); ok && blk.From != "" && m.blockStates[blk.From].Status != "ok" {
			visit(blk.From) // materialize the unrun producer (and its own upstream) first
		}
		order = append(order, id)
	}
	visit(consumerID)
	return order
}

// isShellActionKind reports whether a button kind needs the shell driver /
// orchestrator to act: run (▶), play (run-in-assistant-shell), stop, (view-)diff,
// apply-diff, undo-diff, and the cached regenerate pill. These are the buttons gated
// off shellActionsReady on the async-startup path. Copy (clipboard) and pager-local
// kinds (toggle / confirm / followup) are NOT gated.
func isShellActionKind(kind string) bool {
	switch kind {
	case "run", "play", "stop", "diff", "view-diff", "apply-diff", "undo-diff", "create", "undo-create", "regenerate":
		return true
	}
	return false
}

// editDispatch handles an [edit] button press from either dispatch site
// (mouse-click and keyboard-hint). The two paths are identical, so both sites
// call this helper to de-dup the logic.
//
// No-mux (m.asker == nil): suspend the TUI and open the editor inline via
// tea.ExecProcess; reload on return.
//
// Mux path: spawn the editor in a docked pane via the orchestrator, capture the
// current source mtime, and start the 1-second mtime-poll loop exactly once.
// The poll guard (m.polling) prevents a second [edit] click from spawning a
// second concurrent timer goroutine; the existing loop keeps running and picks
// up any subsequent saves.
func (m model) editDispatch() (model, tea.Cmd) {
	// NO-MUX: suspend the TUI, open the editor, reload on return.
	if m.asker == nil {
		parts := strings.Fields(resolveEditor())
		args := append(parts[1:], m.sourcePath)
		cmd := exec.Command(parts[0], args...)
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return reloadMsg{Err: err} })
	}
	// MUX path: spawn the editor in a docked pane and start the mtime poll.
	if m.orch != nil {
		_ = m.orch.EditSource(resolveEditor(), m.sourcePath)
	}
	if st, err := os.Stat(m.sourcePath); err == nil {
		m.sourceMtime = st.ModTime()
	}
	if !m.polling {
		m.polling = true
		return m, m.sourcePollCmd()
	}
	return m, nil
}

// activateDiffButton handles a "diff" / "view-diff" button press from either
// dispatch site (mouse-click and keyboard-hint). No-mux: renders the in-viewer
// overlay; mux: emits to the orchestrator for the float viewer.
func (m model) activateDiffButton(b Button) (model, tea.Cmd) {
	if m.asker == nil {
		// Store the structured rows (and the parsed files for the narrow fallback);
		// widths/gutters are applied at render time so h/l re-window per frame.
		files := idiff.Parse(b.Payload)
		m.diffFiles = files
		m.diffRows = idiff.Rows(files)
		m.diffMode = true
		m.diffYOff = 0
		m.diffXOff = 0
		m.recomputeDiffGeometry()
		return m, nil
	}
	ac := m.emitAction(b)
	m.reflow()
	return m, tea.Batch(m.flashCmd(), ac)
}

// driftResolveDispatch handles a "resolve manually" press on a DRIFTED diff block.
// The patch no longer applies, so — unlike the read-only view-diff button (that path
// is F30's job) — this opens a CONFLICT-MARKED COPY of the target in $EDITOR: the
// file's current content and the patch's proposed replacement sit side-by-side under
// git-style `-[current]`/`-[patch proposes]` markers, so the user sees WHAT to change
// and WHERE. On save we read the copy back, reconcile it into the REAL target, and
// re-check drift so a completed edit clears the Drifted flag.
//
// If ConflictMarkup can't locate the hunk (ok=false) — or the target can't be read —
// we FALL BACK to the legacy behaviour of opening the raw target file directly, so we
// never regress. driftTempPath!="" is the signal that the temp-file flow is active;
// the reload/poll handlers key off it.
//
// Mirrors editDispatch's two paths: no-mux suspends the TUI and opens the editor
// inline; the mux path spawns a docked editor pane and polls the watched file's mtime.
func (m model) driftResolveDispatch(b Button) (model, tea.Cmd) {
	path := m.driftTargetPath(b.Payload)
	if path == "" {
		m.status = "resolve manually: couldn't determine the diff's target file"
		return m, nil
	}

	// Try to build a conflict-marked copy. tempPath stays "" (→ raw-file fallback) when
	// the target can't be read or no hunk could be located.
	var tempPath string
	if data, err := os.ReadFile(path); err == nil {
		if marked, ok := idiff.ConflictMarkup(string(data), idiff.Parse(b.Payload)); ok {
			// Suffix with the target's extension so the editor gets syntax highlighting.
			if f, err := os.CreateTemp("", "ai-playbook-resolve-*"+filepath.Ext(path)); err == nil {
				_, werr := f.WriteString(marked)
				f.Close()
				if werr == nil {
					tempPath = f.Name()
				} else {
					_ = os.Remove(f.Name())
				}
			}
		}
	}

	// The file we hand the editor: the temp copy when markup succeeded, else the raw target.
	editTarget := path
	if tempPath != "" {
		editTarget = tempPath
	}

	// NO-MUX: suspend the TUI, open the editor, reconcile / re-check drift on return.
	if m.asker == nil {
		parts := strings.Fields(resolveEditor())
		args := append(parts[1:], editTarget)
		cmd := exec.Command(parts[0], args...)
		if tempPath != "" {
			m.driftTempPath = tempPath
			m.driftTempTarget = path
			m.driftTempBlockID = b.BlockID
		}
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return driftResolveReloadMsg{Err: err} })
	}

	// MUX path: spawn the editor in a docked pane and watch the edited file; a save
	// reconciles (temp path) or re-checks drift (fallback). Reuses the shared 1-second
	// mtime-poll loop (the [edit] path's), guarded by m.polling so a second click
	// doesn't spawn a second timer goroutine.
	if m.orch != nil {
		_ = m.orch.EditSource(resolveEditor(), editTarget)
	}
	if st, err := os.Stat(editTarget); err == nil {
		m.driftEditMtime = st.ModTime()
	}
	if tempPath != "" {
		m.driftTempPath = tempPath
		m.driftTempTarget = path
		m.driftTempBlockID = b.BlockID
		m.driftEditPath = "" // the poll watches driftTempPath in this mode
	} else {
		m.driftEditPath = path
	}
	if !m.polling {
		m.polling = true
		return m, m.sourcePollCmd()
	}
	return m, nil
}

// driftResolveFinish reconciles a saved conflict-marked temp copy into the real target
// and re-checks drift. It is the shared read-back → write-back → re-check step for both
// the no-mux ExecProcess callback and the mux mtime-poll. If the user left the openers
// in place (HasConflictMarkers), the edit is treated as UNRESOLVED: the real file is
// left untouched and a status explains why. The temp file is always removed. The real
// target NEVER receives conflict markers — that is the whole point of the indirection.
func (m model) driftResolveFinish() (model, tea.Cmd) {
	temp, target, blockID := m.driftTempPath, m.driftTempTarget, m.driftTempBlockID
	m.driftTempPath, m.driftTempTarget, m.driftTempBlockID = "", "", ""
	if temp == "" {
		return m, m.driftCheckCmds()
	}
	defer os.Remove(temp)

	data, err := os.ReadFile(temp)
	if err != nil {
		m.status = "resolve manually: couldn't read the edited copy — the file was left unchanged"
		return m, nil
	}
	content := string(data)
	if idiff.HasConflictMarkers(content) {
		m.status = "unresolved conflict markers — the file was left unchanged"
		return m, nil
	}

	// Preserve the target's original trailing newline and file mode; capture the prior
	// content so we can tell whether the resolve actually CHANGED anything.
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode()
	}
	orig, _ := os.ReadFile(target)
	if strings.HasSuffix(string(orig), "\n") && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(target, []byte(content), mode); err != nil {
		m.status = "resolve manually: couldn't write the reconciled file"
		return m, nil
	}
	// If the resolve changed the file, flag the block so a still-Drifted re-check verdict
	// is read as a CUSTOM manual resolution (→ Resolved), not unresolved drift. An
	// unchanged "kept current" resolve leaves the flag off, so the block stays Drifted.
	// Also back up the pre-resolve content so the resolved block's Undo can restore it.
	if blockID != "" && content != string(orig) {
		st := m.blockStates[blockID]
		st.pendingResolve = true
		m.blockStates[blockID] = st
		if m.driftResolveBackup == nil {
			m.driftResolveBackup = map[string]string{}
		}
		m.driftResolveBackup[blockID] = string(orig)
	}
	return m, m.driftCheckCmds()
}

// undoResolve reverts a manually-resolved diff block: it restores the target file to its
// pre-resolve (drifted) content from the backup, clears Resolved, and re-checks drift (so
// the block returns to its Drifted state). A custom manual resolution has no git patch to
// reverse, so this file-restore IS the undo.
func (m model) undoResolve(b Button) (model, tea.Cmd) {
	backup, ok := m.driftResolveBackup[b.BlockID]
	if !ok {
		return m, nil
	}
	target := m.driftTargetPath(b.Payload)
	if target == "" {
		m.status = "undo: couldn't determine the diff's target file"
		return m, nil
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode()
	}
	if err := os.WriteFile(target, []byte(backup), mode); err != nil {
		m.status = "undo: couldn't restore the file"
		return m, nil
	}
	delete(m.driftResolveBackup, b.BlockID)
	st := m.blockStates[b.BlockID]
	st.Resolved = false
	m.blockStates[b.BlockID] = st
	return m, m.driftCheckCmds()
}

// driftTargetPath resolves the on-disk path of a patch's target file: the parsed
// new-file path (files[0].NewPath) with a leading a/ or b/ prefix stripped. When an
// orchestrator is wired it delegates to orch.DriftTargetPath so the path resolves
// against the SAME session root the drift check / regenerate use; otherwise (tests /
// degraded) it resolves relative to the process cwd. Returns "" when the patch has no
// parseable target.
func (m model) driftTargetPath(patch string) string {
	if m.orch != nil {
		if p, err := m.orch.DriftTargetPath(patch); err == nil && p != "" {
			return p
		}
	}
	files := idiff.Parse(patch)
	if len(files) == 0 {
		return ""
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(files[0].NewPath, "b/"), "a/")
	if rel == "" || rel == "/dev/null" {
		return ""
	}
	if filepath.IsAbs(rel) {
		return rel
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, rel)
}

// reloadMsg is delivered by tea.ExecProcess after the editor exits (no-mux path)
// to trigger a source reload.
type reloadMsg struct{ Err error }

// driftResolveReloadMsg is delivered by tea.ExecProcess after the $EDITOR opened on
// a drifted patch's TARGET file (the "resolve manually" action, F21) exits. The
// handler re-runs the drift check so a successful manual edit clears the Drifted flag.
type driftResolveReloadMsg struct{ Err error }

// reloadSource re-reads sourcePath through loadPlaybookSource (identical front-matter
// stripping as the initial load), refreshes m.md / m.title / m.subtitle / m.confirmEnv,
// and calls reflow(). blockStates is NOT cleared, so per-block transient state
// (run status, drift flags) survives when block ids are stable across the edit.
// Returns nil immediately when sourcePath is empty (ephemeral playbook).
func (m *model) reloadSource() error {
	if m.sourcePath == "" {
		return nil
	}
	r, title, subtitle, env, err := loadPlaybookSource(m.sourcePath)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.md = string(body)
	m.title = title
	m.subtitle = subtitle
	m.confirmEnv = env
	m.reflow()
	return nil
}

// sourcePollMsg is issued by sourcePollCmd once per second while the mux editor
// pane may be open. The handler stats sourcePath and reloads when the mtime has
// advanced (the user saved in the editor).
type sourcePollMsg struct{}

// sourcePollCmd returns a 1-second tick that delivers a sourcePollMsg. It mirrors
// renderTickCmd and flashCmd — a single fire, re-armed by the handler to keep
// polling as long as the mux [edit] session is active.
func (m model) sourcePollCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return sourcePollMsg{} })
}

// handleToggle flips the Expanded state of the given block and reflows.
// Toggle is pager-local: it never calls emitAction.
func (m model) handleToggle(id string) model {
	st := m.blockStates[id]
	st.Expanded = !st.Expanded
	m.blockStates[id] = st
	m.reflow()
	return m
}
