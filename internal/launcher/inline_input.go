package launcher

import (
	"fmt"
	"os"

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
		// No controlling terminal (pipe/CI): fall back to the line read so a
		// request can still be supplied. (Removed in the cleanup task once the
		// explicit-only contract is accepted.)
		if r, ok := readRequestStdin(os.Stdin, os.Stdout); ok {
			req.UserRequest = r
			cls, cerr := classifyInline(req, func(string) {})
			if cerr != nil {
				cls = author.Classification{Kind: author.KindEscalate}
			}
			return routeInline(req, cls, m)
		}
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
			ch <- input.ThinkUpdate{Done: true}
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
