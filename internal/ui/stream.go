package ui

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// spinTickMsg drives the spinner animation/timer while thinking. gen identifies
// which tick loop issued it: only the loop whose gen == m.tickGen continues, so a
// restartTick (which bumps tickGen) makes any older overlapping loop self-cancel.
type spinTickMsg struct{ gen int }

// tickCmd issues a tick for the CURRENT generation (the streaming hot-path's
// single loop). restartTick uses tickCmdGen to stamp a fresh generation.
func (m model) tickCmd() tea.Cmd { return m.tickCmdGen(m.tickGen) }

func (m model) tickCmdGen(gen int) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinTickMsg{gen: gen} })
}

// hideCursorSeq is DECTCEM "hide cursor" (ESC[?25l). Our pager keeps
// View.Cursor nil forever (see View), and bubbletea's cursed_renderer only
// (re-)emits a cursor-visibility sequence when View.Cursor flips between
// nil/non-nil, or on the first frame / an alt-screen toggle (see
// shouldUpdateCursorVis in cursed_renderer.go). So after the very first frame
// bubbletea NEVER re-hides the cursor. Some multiplexers (zellij) re-SHOW the
// hardware cursor whenever they observe the renderer moving the cursor around
// to paint a diff — which happens in the re-render-heavy states: hint mode, the
// spinner/wave animation, and the verify-success confirm. We counter that by
// re-asserting the hide on those states/transitions and on focus regain.
const hideCursorSeq = "\x1b[?25l" // == ansi.ResetModeTextCursorEnable / ansi.HideCursor

// reassertHideCursor re-emits ESC[?25l via tea.Raw — the supported way to send a
// raw escape sequence. The program serializes RawMsg output through p.flush(),
// which the render-loop goroutine runs BEFORE p.renderer.flush() on the same
// tick, so the sequence can never land inside the renderer's synchronized-output
// (?2026) frame and corrupt it. Re-hiding an already-hidden cursor is a no-op at
// the terminal, so re-asserting every tick does not flicker.
func reassertHideCursor() tea.Cmd { return tea.Raw(hideCursorSeq) }

// startTick returns the single spinner tick loop, or nil if a loop is already
// live. External entry points (Init, thinkEvent, click handlers, regenerate,
// wrap-up, follow-up) call this instead of tickCmd directly so that at most one
// 100ms loop ever exists — overlapping loops would advance the elapsed counter
// multiple times per tick and race the seconds counter. The loop's own continuation (the
// spinTickMsg CONTINUE path) re-issues tickCmd directly; the STOP path clears
// tickRunning.
func (m *model) startTick() tea.Cmd {
	if m.tickRunning {
		return nil
	}
	m.tickRunning = true
	return m.tickCmd()
}

// restartTick force-(re)starts the spinner tick loop for a NEW thinking state,
// even when tickRunning is already set. The re-engagement paths (follow-up,
// regenerate, wrap-up) enter a fresh thinking state whose first stream chunk may
// be minutes away (claude --print is silent until its tool-use phase ends); the
// spinner must animate the whole time. startTick's single-loop guard is correct
// for the streaming hot-path but leaves the follow-up spinner STATIC whenever
// tickRunning is stale-true (e.g. the prior verify-run loop's flag had not yet
// been cleared) — startTick no-ops, so no loop drives the new thinking state.
//
// restartTick bumps tickGen and issues a fresh tickCmd unconditionally. Every
// tickCmd is stamped with the generation it belongs to (spinTickMsg.gen); the
// spinTickMsg handler advances the spinner once per tick but only CONTINUES the
// loop whose gen is current, so any older in-flight loop self-cancels on its next
// fire — exactly one loop survives, no double-counted seconds, and the spinner is
// guaranteed to animate. Use this on the re-engagement entry points; startTick on
// the streaming continuation path.
func (m *model) restartTick() tea.Cmd {
	m.tickGen++
	m.tickRunning = true
	return m.tickCmdGen(m.tickGen)
}

// renderInterval bounds how often streamed text is re-rendered. A stream can
// deliver many small chunks per second; rather than reflow (parse + highlight
// the whole accumulated buffer) and repaint on every chunk — which saturates
// the event loop and stutters — chunks are appended cheaply and a single
// reflow is coalesced per interval (~30fps).
const renderInterval = 33 * time.Millisecond

// renderTickMsg flushes any pending streamed text into a reflow.
type renderTickMsg struct{}

func (m model) renderTickCmd() tea.Cmd {
	return tea.Tick(renderInterval, func(time.Time) tea.Msg { return renderTickMsg{} })
}

// flashCmd returns a command that fires flashTickMsg after ~140ms, clearing
// the active flash highlight.
func (m model) flashCmd() tea.Cmd {
	return tea.Tick(140*time.Millisecond, func(time.Time) tea.Msg { return flashTickMsg{} })
}

// flashTickMsg clears the active flash highlight after ~140ms.
type flashTickMsg struct{}

// flushRender re-renders the accumulated stream buffer if any text is pending,
// pinning the view to the bottom while following. No-op when nothing is dirty,
// so it's cheap to call from the render tick and on EOF.
func (m *model) flushRender() {
	if !m.dirty {
		return
	}
	m.reflow()
	if m.follow {
		m.yOff = len(m.lines) // clampScroll caps to the bottom
		m.clampScroll()
	}
	m.dirty = false
}

func (m model) handleStreamEvents(msg streamEventsMsg) (tea.Model, tea.Cmd) {
	startedThinking := false
	quit := false
	for _, ev := range msg.events {
		switch e := ev.(type) {
		case textEvent:
			if m.structured {
				break // structured authoring: stream carries narration, not the playbook; drain it
			}
			m.md += e.text // cheap append; reflow is coalesced (renderTickMsg)
			m.dirty = true
			// Real playbook content arriving ends the thinking phase: the spinner +
			// activity line stop, and the activity line is cleared so it doesn't
			// linger over the rendered content. Guard against an EMPTY/whitespace-only
			// text event flipping thinking off — claude's stream can interleave empty
			// text chunks during the work phase, and an empty chunk is not real
			// playbook content. Only non-whitespace text ends thinking.
			if strings.TrimSpace(e.text) != "" {
				if m.thinking {
					dbg("textEvent ends thinking: textlen=%d %q", len(e.text), collapseLine(e.text))
				}
				m.thinking = false
				m.progress.SetActivity("")
			}
		case thinkEvent:
			label := e.label
			if label == "" {
				label = m.defaultLabel
			}
			if !m.thinking { // new thinking session: reset the widget
				m.thinking = true
				m.progress.Reset()
				startedThinking = true
			}
			m.thinkLabel = label
		case quitEvent:
			quit = true
		}
	}
	if quit {
		dbg("quitEvent received -> tea.Quit")
		return m, tea.Quit
	}
	if msg.eof {
		m.flushRender() // render whatever's pending immediately
		m.streaming = false
		m.thinking = false
		// A5a-full: a non-EOF stream failure (agent process failed, timed out, or
		// its stream was truncated mid-answer — the fan-out closes the pipe with
		// the producer's wait error) must not read as a clean finish. Surface it
		// in the document (like the re-engage error note) and on the status line;
		// whatever partial content arrived stays visible above it.
		if msg.err != nil {
			dbg("stream ended with error: %v", msg.err)
			m.md += fmt.Sprintf("\n\n_stream error: %v_\n", msg.err)
			m.status = "agent stream failed — partial content shown"
			m.reflow()
			return m, nil
		}
		// Confirm what the agent actually produced: 0 runnable blocks at EOF means
		// it narrated/applied instead of WRITING {id=fix}/{id=verify} blocks (a
		// prompt-compliance gap), vs blocks>0 not visible (a render gap).
		dbg("stream EOF: md=%dB blocks=%d head=%q", len(m.md), len(m.blocks), collapseLine(m.md))
		// Structured mode: the stream carried the agent's narration, not the
		// playbook. On EOF, replace m.md with the captured rendered playbook from
		// bodyProvider so the existing finalDraft processing (preamble-strip,
		// title, junk-guard) runs on it. Reflow immediately so m.blocks is
		// populated — isValidPlaybook requires blocks > 0 for a real playbook.
		if m.structured && m.bodyProvider != nil {
			m.md = m.bodyProvider()
			m.dirty = false // reflow synchronously below — don't defer a render tick
			m.reflow()
		}
		// Finalized-playbook draft: strip any preamble above the H1 title and set
		// the pager header to the playbook title. Gated on finalDraft so a
		// troubleshoot transcript (non-finalDraft EOF) is left untouched (default
		// "ai-playbook — <harness>" header, no stripping).
		if m.finalDraft {
			title, body := playbookHeading(m.md)
			// Safety guard: occasionally the model NARRATES instead of producing a
			// playbook (e.g. a short "The playbook is above…" with 0 runnable blocks).
			// A real playbook has an H1 title AND at least one runnable block. If this
			// draft is NOT a real playbook, restore the troubleshoot we replaced and
			// do NOT keep or persist the junk.
			// NOTE: a structured playbook with no runnable block would be junk-guarded here; troubleshooting playbooks always carry a fix/verify block (revisit in B2/B3).
			if !isValidPlaybook(m.md, len(m.blocks)) {
				dbg("invalid final playbook (title=%q blocks=%d) — restoring troubleshoot, skipping persist", title, len(m.blocks))
				m.md = m.preFinalMd
				m.title = ""
				m.finalDraft = false
				m.status = "Couldn't generate a clean playbook — kept the troubleshoot. Press c to retry."
				m.reflow()
				return m, nil
			}
			// VALID: strip any preamble above the H1 and set the pager header title.
			m.md = body
			m.title = title
			m.reflow()
		}
		// Close a live in-process re-engagement stream so the agent process is
		// reaped and the orchestrator's on-close side effects fire (regenerate's
		// cache re-store, wrap-up's artifact close). No-op when no stream is active (nil).
		if m.reengageStream != nil {
			_ = m.reengageStream.Close()
			m.reengageStream = nil
		}
		// Site 2: diff blocks are now fully parsed (reflow ran above). Fire async
		// drift checks so the badge appears without blocking the event loop.
		// Assisted (GUIDED) entry: m.md/m.blocks are now final for this EOF (the
		// structured/finalDraft branches above have already rewritten m.md and
		// reflowed if applicable) — safe to compute the first ready block now.
		m, startCmd := m.maybeStartAssisted()
		return m, tea.Batch(startCmd, m.driftCheckCmds())
	}
	cmds := []tea.Cmd{readStream(m.reader, m.parser)}
	if startedThinking {
		cmds = append(cmds, m.startTick())
	}
	// Coalesce the (expensive) whole-buffer reflow to renderInterval instead
	// of reflowing on every chunk. Schedule at most one tick at a time.
	if m.dirty && !m.renderScheduled {
		m.renderScheduled = true
		cmds = append(cmds, m.renderTickCmd())
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleSpinTick(msg spinTickMsg) (tea.Model, tea.Cmd) {
	// Stale loop: a restartTick bumped the generation, so a newer loop now drives
	// the spinner. Drop this tick WITHOUT advancing the frame or seconds (the
	// live loop already does both) and do NOT continue — it self-cancels here,
	// leaving exactly one live loop and no double-counted seconds.
	if msg.gen != m.tickGen {
		return m, nil
	}
	running := false
	for id, st := range m.blockStates {
		if st.Status == "running" || st.Status == "regenerating" || st.RollingBack {
			st.SpinFrame++
			m.blockStates[id] = st
			running = true
		}
	}
	if m.thinking {
		m.progress.Tick()
	}
	if !m.thinking && !running {
		m.tickRunning = false
		return m, nil
	}
	// B1c: advancing SpinFrame above is enough — the run-region spinner is
	// regenerated from the current frame at View time (spinRow), so a spin tick
	// no longer triggers a full-document reflow/Render just to move one glyph. A
	// real state change (block finishes, drift, etc.) still reflows on its own
	// message. The thinking spinner likewise overlays at View (progress widget).
	// Re-assert the hide-cursor on every live tick: the spinner/wave diff this
	// tick paints is exactly the renderer activity that makes zellij re-show the
	// hardware cursor. Idempotent, so it never flickers (see reassertHideCursor).
	return m, tea.Batch(m.tickCmd(), reassertHideCursor())
}

func (m model) handleActivity(msg activityMsg) (tea.Model, tea.Cmd) {
	// One agent tool-call summary off the activity feed. A summary from a STALE
	// feed (m.activity was swapped to a fresh re-engagement channel since this
	// wait was issued) is ignored — don't paint it and don't re-subscribe to the
	// dead channel. msg.ch == nil is the legacy/no-source case (always current).
	if msg.ch != nil && msg.ch != m.activity {
		return m, nil
	}
	// Channel closed (!ok): the current feed is torn down — stop re-subscribing.
	// Otherwise record the latest summary (shown under the "Working…" line ONLY
	// while thinking, so a late summary never paints over settled content) and
	// wait for the next one.
	if !msg.ok {
		m.activity = nil
		return m, nil
	}
	if m.thinking {
		// The feed now carries the model's live REASONING as well as tool
		// summaries (agentstream Reasoning/ToolActivity). Reasoning can be long or
		// multi-line; SetActivity collapses to one trimmed line and the render
		// truncates it to the column width.
		m.progress.SetActivity(msg.summary)
	}
	return m, m.activityWaitCmd()
}

func (m model) handleReArm(msg reArmStreamMsg) (tea.Model, tea.Cmd) {
	// In-process re-arm: swap the parser to the fresh re-engagement stream
	// (regenerate/followup/wrapup). The orchestrator already produced the stream
	// off the event loop; here we point the reader at it and resume streaming.
	// The stream's Closer is held so EOF reaps the agent + fires the
	// orchestrator's on-close side effects.
	dbg("re-arm (in-process): reader ready err=%v", msg.err)
	if msg.err != nil {
		m.thinking = false
		m.md += fmt.Sprintf("\n\n_re-engage error: %v_\n", msg.err)
		m.reflow()
		return m, nil
	}
	if m.reengageStream != nil {
		_ = m.reengageStream.Close()
	}
	// Reset block run-states for the fresh round: the re-authored playbook reuses
	// ids (id=fix, id=verify, …), so stale states from the prior round would
	// otherwise paint "failed"/"succeeded" onto the new, not-yet-run blocks.
	dbg("re-arm: clearing %d stale block states for the fresh round", len(m.blockStates))
	clear(m.blockStates)
	m.reengageStream = msg.reader
	m.reader = bufio.NewReader(msg.reader)
	m.parser = &streamParser{}
	cmds := []tea.Cmd{readStream(m.reader, m.parser)}
	// Swap the activity feed to the re-engagement's live reasoning + tool feed and
	// re-subscribe, so EVERY round (followup/regenerate/wrapup) shows live reasoning
	// on the activity line, exactly like the initial authoring.
	//
	// Issue #2 (live activity on repeat rounds): each re-engagement round's
	// orchestrator fan-out (orchestrator.Followup/Regenerate/Wrapup → agentstream.
	// FanOut) yields a FRESH activity channel; the ui MUST swap m.activity to it
	// and issue a fresh activityWaitCmd unconditionally. Critically this must NOT
	// be gated on the PRIOR feed's liveness: by the 2nd follow-up the 1st round's
	// channel has already drained+closed, so m.activity is nil and there is NO live
	// wait. A swap that re-subscribes only "when the previous one is alive" would
	// leave the 2nd round with a dead activity line (the reported symptom — a long
	// silent wait, then text). The fresh wait captures the just-swapped channel, so
	// a stale in-flight wait from the prior round resolves against its own (now
	// different) channel and is dropped by the activityMsg stale-guard — it can
	// never clobber this fresh subscription.
	//
	// A nil activity (text-fallback round only) leaves the previous subscription
	// untouched — there is no live feed to swap in.
	if msg.activity != nil {
		m.activity = msg.activity
		m.progress.SetActivity("")
		cmds = append(cmds, m.activityWaitCmd())
	}
	// Drift is re-checked at stream-EOF (Site 2) once the regenerated blocks land.
	return m, tea.Batch(cmds...)
}
