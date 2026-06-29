// create_progress.go — the `create` inline-progress host and the changed author
// path. `create <prompt>` no longer streams the building playbook into the
// fullscreen viewer; instead it shows the viewer-style indicator (spinner +
// Waiting… + elapsed + model-activity line) INLINE below the shell prompt while
// the agent authors, drains the authoring stream to collect the COMPLETE playbook,
// persists it (cache + the store-file commit wired through the viewer's Reengage),
// and only THEN opens the fullscreen viewer with the finished playbook. The flow
// is identical with or without a multiplexer.
//
// The agent's `ask` tool is fully supported during authoring: with a mux the float
// hosts it (never reaching the bridge); with no mux the bridge delivers it here and
// the host pauses the progress line, embeds input.Ask, responds, and resumes.
package launcher

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/colorprofile"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/askbridge"
	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/input"
	"github.com/Townk/ai-playbook/internal/orchestrator"
	"github.com/Townk/ai-playbook/internal/playbook"
	"github.com/Townk/ai-playbook/internal/triage"
	"github.com/Townk/ai-playbook/internal/ui"
)

// ── progress+ask host ───────────────────────────────────────────────────────

// progressAskModel renders ui.WaitingLine inline (no alt-screen) while the
// authoring stream is drained in the background, AND — when a no-mux ask bridge is
// present — subscribes to its request channel. On a request it PAUSES the waiting
// line and embeds input.Ask (the same dialog the float/overlay use), responds via
// the bridge, then re-arms and resumes the waiting line. It quits on a done signal
// (the drain reached EOF). mux-present asks go to the float and never reach the
// bridge, so reqCh is simply nil there.
type progressAskModel struct {
	width int
	pw    ui.ProgressWidget

	// channels driving the model.
	act  <-chan string
	done <-chan struct{}
	reqs <-chan askbridge.Request

	// embedded ask (no-mux): non-nil while an ask is open (the waiting line is paused).
	ask    *input.Ask
	askReq askbridge.Request
}

func newProgressAskModel(act <-chan string, done <-chan struct{}, reqs <-chan askbridge.Request) progressAskModel {
	return progressAskModel{width: 80, act: act, done: done, reqs: reqs}
}

// host messages.
type paTickMsg struct{}
type paActMsg struct {
	s  string
	ok bool
}
type paDoneMsg struct{}
type paAskMsg struct{ req askbridge.Request }

func (m progressAskModel) Init() tea.Cmd {
	return tea.Batch(paTick(), paRecvAct(m.act), paRecvDone(m.done), paRecvAsk(m.reqs))
}

func paTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return paTickMsg{} })
}

func paRecvAct(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		s, ok := <-ch
		return paActMsg{s: s, ok: ok}
	}
}

func paRecvDone(ch <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return paDoneMsg{}
	}
}

// paRecvAsk blocks for the next pending ask on the bridge; a nil channel yields nil
// (the mux-present path never subscribes).
func paRecvAsk(ch <-chan askbridge.Request) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return paAskMsg{req: <-ch} }
}

func (m progressAskModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case paTickMsg:
		m.pw.Tick()
		return m, paTick()
	case paActMsg:
		if msg.ok && msg.s != "" {
			m.pw.SetActivity(msg.s)
		}
		if !msg.ok {
			return m, nil // activity channel closed; stop re-arming
		}
		return m, paRecvAct(m.act)
	case paDoneMsg:
		return m, tea.Quit
	case paAskMsg:
		// Pause the waiting line and embed the ask dialog (no re-arm until it resolves).
		m.askReq = msg.req
		m.ask = input.NewAsk("ai-playbook", msg.req.Prompt, "", msg.req.Type, msg.req.Choices)
		return m, m.ask.Init()
	}
	// While an ask is open, route everything else (key presses, field cmds) to it.
	if m.ask != nil {
		cmd, done, submitted, value := m.ask.Update(msg)
		if !done {
			return m, cmd
		}
		m.askReq.Respond(askbridge.Answer{Value: value, Submitted: submitted})
		m.ask = nil
		// Resume the waiting line and re-arm for the next ask.
		return m, paRecvAsk(m.reqs)
	}
	return m, nil
}

func (m progressAskModel) View() tea.View {
	if m.ask != nil {
		return tea.NewView(m.ask.View(input.FloatWidthDefault))
	}
	return tea.NewView(m.pw.Render(m.width))
}

// lastHeight is the rendered line count for the clear-on-exit step. At exit the ask
// is never open (quit fires on done, after authoring completed), so this mirrors the
// waiting-line height; it also covers an open ask defensively.
func (m progressAskModel) lastHeight() int {
	if m.ask != nil {
		return strings.Count(m.ask.View(input.FloatWidthDefault), "\n") + 1
	}
	return strings.Count(m.pw.Render(m.width), "\n") + 1
}

// runCreateProgressFn is the progress-host seam: production drives the inline tea
// program on /dev/tty; tests override it to run headless (drain activity + answer
// any bridge asks) so createAuthorWithProgress is testable without a TTY/model.
var runCreateProgressFn = runCreateProgress

// runCreateProgress renders the inline progress host on /dev/tty, fed by the
// activity feed + the (optional) no-mux ask bridge + the done signal. With no
// controlling terminal it can't render the inline UI, so it simply waits for done
// while auto-cancelling any bridge ask (so the agent never blocks forever).
func runCreateProgress(activity <-chan string, bridge *askbridge.Bridge, done <-chan struct{}) {
	var reqs <-chan askbridge.Request
	if bridge != nil {
		reqs = bridge.Requests()
	}

	tty, terr := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if terr != nil {
		for {
			select {
			case req := <-reqs:
				req.Respond(askbridge.Answer{Submitted: false})
			case <-done:
				return
			}
		}
	}
	defer tty.Close()

	fm, perr := tea.NewProgram(
		newProgressAskModel(activity, done, reqs),
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
	if pm, ok := fm.(progressAskModel); ok {
		input.ClearInline(tty, pm.lastHeight())
	}
	if perr != nil {
		// The program failed to run; nothing inline was shown. The drain still owns the
		// stream, so just wait for completion so the body is collected.
		<-done
	}
}

// ── create author path (inline progress, then viewer) ───────────────────────

// createStream bundles the authoring event stream's surfaces: the playbook reader
// (drained to EOF so the body accumulates — NOT rendered live), the model-activity
// feed, a Body accessor valid after the reader hits EOF, and a teardown.
type createStream struct {
	reader   io.ReadCloser
	activity <-chan string
	body     func() string
	close    func()
}

// createStreamFn is the author-stream seam: production runs the owned AuthorEvents
// invocation and fans it out; tests inject a canned reader/activity/body so the
// create core runs without a live harness.
var createStreamFn = structuredStream

// structuredStream is the SHARED structured-authoring core for both create (inline
// progress host) and escalate (in-viewer structured RunStream): it opens the owned
// authoring event stream (AuthorEvents with Structured: true — the agent authors via
// submit_playbook DATA, not {id=…} markdown) wired to the session's tools backend and
// fans it into a reader + activity feed + body accumulator. The body() closure prefers
// the deterministic render of the captured playbook (sess.lastPB) over the accumulated
// stream text (see structuredBody). A nil session authors without tools (writeMCPConfig
// is nil-safe).
func structuredStream(req capture.Request, sess *session, cfg *config.Config) (createStream, error) {
	mcpPath, removeMCP := sess.writeMCPConfig()
	events, closeFn, err := author.AuthorEvents(req, author.AuthorOptions{
		Cfg:           cfg,
		MCPConfigPath: mcpPath,
		Structured:    true, // author via submit_playbook (DATA), not {id=…} markdown
	})
	if err != nil {
		removeMCP()
		return createStream{}, err
	}
	reader, activity, fo := agentstream.FanOut(events, closeFn, ActivityBuffer)
	home, _ := os.UserHomeDir()
	return createStream{
		reader:   reader,
		activity: activity,
		body:     func() string { return structuredBody(sess, req.ProjectRoot, home, fo.Body) },
		close:    func() { reader.Close(); removeMCP() },
	}, nil
}

// structuredBody resolves the authored body in structured mode, shared by create's
// inline progress host and escalate's in-viewer structured RunStream. It prefers the
// DETERMINISTIC render of the captured structured playbook (the agent submitted it via
// submit_playbook → OnPlaybook → sess.lastPB). It falls back to the accumulated stream
// text (fallback, typically the fan-out's Body) only when the model misbehaved and
// submitted nothing, so authoring never dead-ends. projectRoot + home are the authoring
// machine's project root and home dir — used to Portabilize a project_bound playbook
// (replace absolute paths with shell variables) before rendering.
func structuredBody(sess *session, projectRoot, home string, fallback func() string) string {
	if sess != nil {
		if last := sess.lastPB.Load(); last != nil {
			pb := *last
			if pb.Meta.ProjectBound {
				playbook.Portabilize(&pb, projectRoot, home)
			}
			return playbook.Render(pb)
		}
	}
	if fallback != nil {
		return fallback()
	}
	return ""
}

// createViewFn is the phase-2 viewer seam: production writes the complete playbook
// to a temp file and opens ui.Main (reshaped to `run --file`) driving the authoring
// session's shell; tests override it to capture the body handed to the viewer.
var createViewFn = createViewPlaybook

// newCreateReengage builds the re-engagement context the create flow persists under
// and threads into the phase-2 viewer (so the viewer's run blocks drive the session
// shell and the `w`-key wrap-up CommitPlaybook writes the store file). create's newCreateReengage
// and escalate's inline Reengage (session.go) are field-identical: StoreDir from cfg.GlobalStoreDir()
// and the createDecision cache keys (only when the cache wasn't disabled/bypassed).
func newCreateReengage(req capture.Request, d triage.Decision, c *cache.Cache, noCache bool, sess *session, cfg *config.Config) *orchestrator.Reengage {
	var sharedDrv *driver.Driver
	if sess != nil {
		sharedDrv = sess.drv
	}
	re := &orchestrator.Reengage{
		Req:         req,
		Agent:       sess.authoringAgent(),
		Events:      buildReengageEvents(req, sess),
		Cache:       c,
		RequestJSON: requestJSON(req),
		Metadata:    capturedMetaSeam(sess),
		EnvLookup:   buildEnvLookup(sharedDrv),
		StoreDir:    cfg.GlobalStoreDir(),
	}
	if !d.Disabled && !noCache {
		re.CtxHash = d.CtxHash
		re.ReqHash = d.ReqHash
	}
	return re
}

// capturedMetaSeam returns the structured playbook's meta as the front-matter
// classification — NO metadata model pass. The create flow folds classification
// into the single submit_playbook call, so the captured pb's Meta is authoritative.
// Falls back to the model classifier (buildMetadataSeam) only if create produced no
// structured playbook (the text-fallback path), so the commit still gets a
// classification.
func capturedMetaSeam(sess *session) func(doc string) (orchestrator.PlaybookMeta, error) {
	return func(doc string) (orchestrator.PlaybookMeta, error) {
		if sess != nil {
			if last := sess.lastPB.Load(); last != nil {
				m := last.Meta
				notes := make(map[string]string, len(m.Env))
				for _, ev := range m.Env {
					if ev.Name != "" {
						notes[ev.Name] = ev.Why
					}
				}
				return orchestrator.PlaybookMeta{
					Description:  m.Description,
					Category:     m.Category,
					Tags:         m.Tags,
					ProjectBound: m.ProjectBound,
					EnvNotes:     notes,
				}, nil
			}
		}
		return buildMetadataSeam(sess)(doc)
	}
}

// createAuthorWithProgress is the changed `create` author path: open the owned
// authoring event stream, DRAIN it to collect the COMPLETE playbook body while the
// inline progress host (spinner + activity + the no-mux ask) renders on /dev/tty,
// persist the body (cache store now + the store-file commit wired through the
// viewer's Reengage), then open the fullscreen viewer with the finished playbook.
// On a stream-start failure (harness missing/unsupported) it falls back to the
// existing text author path. NO triage / classify / cache-hit serve is consulted.
func createAuthorWithProgress(req capture.Request, d triage.Decision, c *cache.Cache, noCache bool, sess *session, cfg *config.Config) int {
	cwd := reqCwd(req)
	var sharedDrv *driver.Driver
	if sess != nil {
		sharedDrv = sess.drv
	}
	reengage := newCreateReengage(req, d, c, noCache, sess, cfg)

	cs, err := createStreamFn(req, sess, cfg)
	if err != nil {
		// Harness binary missing / unsupported: author via the existing text path so
		// authoring still works (no inline progress, but the playbook is produced).
		dbg("createAuthorWithProgress: author stream failed (%v); text fallback", err)
		return authorPlaybookText(req, d, c, noCache, reengage, cwd, sharedDrv, "", bridgeOf(sess), cfg.Driver.Shell)
	}
	defer cs.close()

	// Drain the playbook reader to EOF in the background so the fan-out accumulates
	// the body — the building playbook is NOT rendered (the inline host shows only the
	// spinner + activity). Closing done quits the progress host.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, cs.reader)
	}()
	runCreateProgressFn(cs.activity, bridgeOf(sess), done)

	body := cs.body() // valid after the reader hit EOF (the drain finished)

	// Cache-store on completion — replicates authorPlaybook's persist tail exactly
	// (skipped when the cache was disabled/bypassed or the keys/body are empty). The
	// store-file commit happens later, through the viewer's Reengage on wrap-up.
	if !d.Disabled && !noCache && d.CtxHash != "" && d.ReqHash != "" && body != "" {
		if _, serr := c.Store(d.CtxHash, d.ReqHash, "playbook", body, nil, requestJSON(req)); serr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook create: cache store: %v\n", serr)
		}
	}

	return createViewFn(body, sess, reengage, cfg, req)
}

// createViewPlaybook opens the fullscreen viewer with the COMPLETE playbook (no
// live streaming): write the body to a temp file, reuse the authoring session's
// driver (so run blocks execute in the same shell the agent authored in), thread the
// Reengage (regenerate / `w`-wrap-up commit) + the no-mux ask bridge, reshape os.Args
// to `run --file <tmp>` (no --cached → no badge), and call ui.Main. The temp file is
// cleaned up after the viewer returns.
func createViewPlaybook(body string, sess *session, re *orchestrator.Reengage, cfg *config.Config, req capture.Request) int {
	f, err := os.CreateTemp("", "apb-create-*.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook create: %v\n", err)
		return 1
	}
	tmp := f.Name()
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "ai-playbook create: %v\n", err)
		return 1
	}
	f.Close()
	defer os.Remove(tmp)

	cwd := reqCwd(req)

	// Reuse the session's shared driver (the same shell the tools backend exposes) so
	// the viewer's run blocks drive it; Main does NOT close a session-supplied driver.
	if sess != nil {
		ui.SetDriver(sess.drv)
	}
	ui.SetShell(cfg.Driver.Shell)   // own-driver fallback honors the configured shell
	ui.SetReengage(re)              // regenerate / `w`-wrap-up CommitPlaybook (store file)
	ui.SetAskBridge(bridgeOf(sess)) // no-mux re-engagement `ask` → in-viewer overlay
	ui.SetFinalDraft(true)          // the rendered structured playbook IS a final draft: `w` persists (no re-generate)
	ui.SetAsker(sess.asker(cwd))    // `f` proactive amend — same asker the troubleshoot viewer gets (float in mux; no-op in no-mux)

	argv := []string{os.Args[0], "run"}
	if cwd != "" {
		argv = append(argv, "--cwd", cwd)
	}
	argv = append(argv, "--file", tmp)
	os.Args = argv
	return uiMainFn()
}
