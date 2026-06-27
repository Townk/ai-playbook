package launcher

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/colorprofile"

	tea "charm.land/bubbletea/v2"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/input"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/ui"
)

// inlineClassifyFn is the in-box classify seam (tests inject a fake).
var inlineClassifyFn classifyFunc = author.ClassifyRequest

// routeInlineSessionFn / viewProseFn are the escalate / answer seams (tests
// override them to avoid a live driver / TTY).
var (
	routeInlineSessionFn = runSession
	viewProseFn          = viewProse
)

// routeInline routes a KNOWN classification on the null-mux inline path (the
// in-box classify already ran): command → stdout (the ZLE widget captures it,
// or a human reads it); answer/escalate → the fullscreen viewer (Phase 2).
func routeInline(req capture.Request, cls author.Classification, m mux.Mux) int {
	switch cls.Kind {
	case author.KindCommand:
		fmt.Println(cls.Content)
		return 0
	case author.KindAnswer:
		return viewProseFn(cls.Content, cls.Title, reqCwd(req))
	default: // escalate (incl. empty/unknown)
		return routeInlineSessionFn(req, cls.Title, m)
	}
}

// reqCwd is the request's effective working dir (project root, else cwd).
func reqCwd(req capture.Request) string {
	if req.ProjectRoot != "" {
		return req.ProjectRoot
	}
	return req.CWD
}

// classifyInline runs the cheap classify, streaming a one-line, whitespace-
// collapsed tail of the model output to onTail (so the in-box thinking line
// shows the answer/command forming). Reuses the launcher's extractJSONContent +
// thinkingTail helpers.
func classifyInline(req capture.Request, onTail func(string)) (author.Classification, error) {
	cfg, _ := config.Load()
	return inlineClassifyFn(req, author.AuthorOptions{
		Cfg: cfg,
		OnText: func(acc string) {
			onTail(thinkingTail(extractJSONContent(acc), thinkingTailRunes))
		},
	})
}

// viewProse renders a short prose answer in the fullscreen viewer in-process:
// write it to a temp markdown file and reuse ui.Main via the os.Args-reshaped
// `run` entry (the same reshape serveCachedPlaybook/AnswerMain use).
func viewProse(content, title, cwd string) int {
	f, err := os.CreateTemp("", "aapb-inline-answer-*.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook: %v\n", err)
		return 1
	}
	tmp := f.Name()
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "ai-playbook: %v\n", err)
		return 1
	}
	f.Close()
	defer os.Remove(tmp)

	// ui.Main opens its own driver for this reshaped `run`; honor the configured shell.
	cfg, _ := config.Load()
	ui.SetShell(cfg.Driver.Shell)

	argv := []string{os.Args[0], "run"}
	if title != "" {
		argv = append(argv, "--title", title)
	}
	if cwd != "" {
		argv = append(argv, "--cwd", cwd)
	}
	argv = append(argv, tmp)
	os.Args = argv
	return ui.Main()
}

// inlineRunFn is the inline-input seam (tests override it to avoid a TTY).
var inlineRunFn = input.RunInline

// inlineInput renders the input UI inline below the shell prompt, classifies the
// submitted request IN-BOX (sine-wave), then routes the result. Cancel (Esc)
// exits cleanly (0). The classification is delivered back over a buffered channel
// read after RunInline returns, so the tea goroutine and the launcher never share
// a variable.
func inlineInput(req capture.Request, m mux.Mux) int {
	type clsResult struct {
		cls author.Classification
		err error
	}
	clsCh := make(chan clsResult, 1)

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		// No controlling terminal: the inline box needs a TTY. Supply the request
		// explicitly (CLI arg / $AI_PLAYBOOK_USER_REQUEST) in non-interactive
		// contexts. Nothing to do here.
		fmt.Fprintln(os.Stderr, "ai-playbook: no terminal for inline input; pass the request as an argument")
		return 0
	}
	defer tty.Close()

	res, rerr := inlineRunFn(tty, input.InlineRequest{
		Title:   "ai-playbook",
		Prompt:  "How can I help you today?",
		Value:   prefillTemplate(req),
		History: requestHistoryPath(),
	}, func(value string) <-chan input.ThinkUpdate {
		ch := make(chan input.ThinkUpdate, 8)
		go func() {
			defer close(ch)
			r := req
			r.UserRequest = value
			cls, cerr := classifyInline(r, func(line string) {
				select {
				case ch <- input.ThinkUpdate{Line: line}:
				default: // drop if the model hasn't drained — the line is cosmetic
				}
			})
			clsCh <- clsResult{cls: cls, err: cerr}
			select {
			case ch <- input.ThinkUpdate{Done: true}:
			default: // reader cancelled (e.g. esc during classify wave) — don't block
			}
		}()
		return ch
	})
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook: inline input: %v\n", rerr)
		return 1
	}
	if !res.Submitted {
		return 0 // cancelled — nothing to route
	}
	req.UserRequest = res.Value
	r := <-clsCh // set by the classify goroutine before it sent Done; race-free
	cls := r.cls
	if r.err != nil {
		cls = author.Classification{Kind: author.KindEscalate}
	}
	return routeInline(req, cls, m)
}

// explicitProgress is the explicit-request null-mux path: SKIP the input box,
// show the viewer-style "Working…" indicator + model-activity line while the
// cheap classify runs inline, then route the result. A classify error escalates.
func explicitProgress(req capture.Request, m mux.Mux) int {
	cls, err := classifyWithProgress(req)
	if err != nil {
		cls = author.Classification{Kind: author.KindEscalate}
	}
	return routeInline(req, cls, m)
}

// classifyWithProgress runs classifyInline while rendering ui.WaitingLine inline
// (non-alt-screen) on /dev/tty. With no controlling terminal it classifies
// silently. The inline region is cleared before returning.
func classifyWithProgress(req capture.Request) (author.Classification, error) {
	tty, terr := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if terr != nil {
		return classifyInline(req, func(string) {})
	}
	defer tty.Close()

	resCh := make(chan wResult, 1)
	actCh := make(chan string, 8)
	go func() {
		cls, cerr := classifyInline(req, func(line string) {
			select {
			case actCh <- line:
			default:
			}
		})
		resCh <- wResult{cls: cls, err: cerr}
		close(actCh)
	}()

	fm, perr := tea.NewProgram(
		newWaitingModel(actCh, resCh),
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithColorProfile(colorprofile.TrueColor),
	).Run()
	wm, _ := fm.(waitingModel)
	input.ClearInline(tty, wm.lastHeight())
	if perr != nil {
		r := <-resCh
		return r.cls, r.err
	}
	return wm.res.cls, wm.res.err
}

// wResult carries the classify outcome out of the goroutine.
type wResult struct {
	cls author.Classification
	err error
}

// waitingModel messages.
type wTickMsg struct{}
type wActMsg string
type wDoneMsg struct{ r wResult }

// waitingModel is a minimal bubbletea model that renders ui.WaitingLine inline
// (no alt-screen) while classifyInline runs in a goroutine.
type waitingModel struct {
	width    int
	frame    int
	ticks    int // 100ms ticks; seconds = ticks/10
	activity string
	res      wResult
	actCh    <-chan string
	resCh    <-chan wResult
}

func newWaitingModel(actCh <-chan string, resCh <-chan wResult) waitingModel {
	return waitingModel{width: 80, actCh: actCh, resCh: resCh}
}

func (m waitingModel) Init() tea.Cmd {
	return tea.Batch(wTick(), wRecvAct(m.actCh), wRecvDone(m.resCh))
}

func wTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return wTickMsg{} })
}

func wRecvAct(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		s, ok := <-ch
		if !ok {
			return wActMsg("")
		}
		return wActMsg(s)
	}
}

func wRecvDone(ch <-chan wResult) tea.Cmd {
	return func() tea.Msg { return wDoneMsg{r: <-ch} }
}

func (m waitingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case wTickMsg:
		m.frame++
		m.ticks++
		return m, wTick()
	case wActMsg:
		if string(msg) != "" {
			m.activity = string(msg)
		}
		return m, wRecvAct(m.actCh)
	case wDoneMsg:
		m.res = msg.r
		return m, tea.Quit
	}
	return m, nil
}

func (m waitingModel) View() tea.View {
	return tea.NewView(ui.WaitingLine(m.frame, m.ticks/10, m.activity, m.width))
}

// lastHeight is the rendered line count for the clear-on-exit step.
func (m waitingModel) lastHeight() int {
	if m.activity == "" {
		return 1
	}
	return 2
}
