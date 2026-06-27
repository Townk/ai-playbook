package launcher

import (
	"os"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/input"
	"github.com/Townk/ai-playbook/internal/mux"
)

func TestExplicitProgress_ClassifiesAndRoutes(t *testing.T) {
	isolateCache(t)
	origCls, origSess := inlineClassifyFn, routeInlineSessionFn
	t.Cleanup(func() { inlineClassifyFn, routeInlineSessionFn = origCls, origSess })
	inlineClassifyFn = fakeClassify(author.Classification{Kind: author.KindEscalate}, nil)
	routeInlineSessionFn = func(_ capture.Request, _ string, _ mux.Mux) int { return 9 }

	if code := explicitProgress(capture.Request{CWD: "/p", UserRequest: "why did it crash"}, mux.Null()); code != 9 {
		t.Fatalf("explicitProgress exit = %d, want 9 (escalate seam)", code)
	}
}

func TestExplicitProgress_ClassifyErrorEscalates(t *testing.T) {
	isolateCache(t)
	origCls, origSess := inlineClassifyFn, routeInlineSessionFn
	t.Cleanup(func() { inlineClassifyFn, routeInlineSessionFn = origCls, origSess })
	inlineClassifyFn = fakeClassify(author.Classification{}, os.ErrDeadlineExceeded)
	called := false
	routeInlineSessionFn = func(_ capture.Request, _ string, _ mux.Mux) int { called = true; return 0 }

	if code := explicitProgress(capture.Request{CWD: "/p"}, mux.Null()); code != 0 || !called {
		t.Fatalf("classify error must escalate; exit=%d called=%v", code, called)
	}
}

func TestRouteInline_Command_PrintsToStdout(t *testing.T) {
	isolateCache(t)
	var code int
	out := captureStdout(t, func() {
		code = routeInline(capture.Request{CWD: "/p"},
			author.Classification{Kind: author.KindCommand, Content: "git status"}, mux.Null())
	})
	if code != 0 {
		t.Fatalf("command exit = %d, want 0", code)
	}
	if !strings.Contains(out, "git status") {
		t.Errorf("command stdout = %q, want the command", out)
	}
}

func TestRouteInline_Answer_GoesToViewer(t *testing.T) {
	isolateCache(t)
	orig := viewProseFn
	t.Cleanup(func() { viewProseFn = orig })
	var gotContent, gotTitle string
	viewProseFn = func(content, title, cwd string) int { gotContent, gotTitle = content, title; return 0 }

	code := routeInline(capture.Request{CWD: "/p"},
		author.Classification{Kind: author.KindAnswer, Content: "HEAD is the tip.", Title: "git head"}, mux.Null())
	if code != 0 {
		t.Fatalf("answer exit = %d, want 0", code)
	}
	if gotContent != "HEAD is the tip." || gotTitle != "git head" {
		t.Errorf("viewProse got (%q,%q), want the prose + title", gotContent, gotTitle)
	}
}

func TestRouteInline_Escalate_RunsSession(t *testing.T) {
	isolateCache(t)
	orig := routeInlineSessionFn
	t.Cleanup(func() { routeInlineSessionFn = orig })
	routeInlineSessionFn = func(_ capture.Request, _ string, _ mux.Mux) int { return 42 }

	if code := routeInline(capture.Request{CWD: "/p"},
		author.Classification{Kind: author.KindEscalate}, mux.Null()); code != 42 {
		t.Fatalf("escalate exit = %d, want 42 (session seam)", code)
	}
}

func TestClassifyInline_StreamsContentTail(t *testing.T) {
	isolateCache(t)
	orig := inlineClassifyFn
	t.Cleanup(func() { inlineClassifyFn = orig })
	inlineClassifyFn = func(_ capture.Request, opts author.AuthorOptions) (author.Classification, error) {
		if opts.OnText != nil {
			opts.OnText(`{"kind":"command","content":"git st`)
		}
		return author.Classification{Kind: author.KindCommand, Content: "git status"}, nil
	}
	var lastLine string
	cls, err := classifyInline(capture.Request{}, func(line string) { lastLine = line })
	if err != nil {
		t.Fatal(err)
	}
	if cls.Kind != author.KindCommand {
		t.Fatalf("kind = %q, want command", cls.Kind)
	}
	if !strings.Contains(lastLine, "git st") {
		t.Errorf("tail = %q, want the forming content", lastLine)
	}
}

// On null mux with no explicit request, Assist must use the inline-input
// seam (NOT the float launch, NOT the stdin read). We assert via the seam.
func TestInlineInput_SubmitRoutesClassification(t *testing.T) {
	isolateCache(t)
	origRun, origCls, origSess := inlineRunFn, inlineClassifyFn, routeInlineSessionFn
	t.Cleanup(func() {
		inlineRunFn, inlineClassifyFn, routeInlineSessionFn = origRun, origCls, origSess
	})
	inlineClassifyFn = fakeClassify(author.Classification{Kind: author.KindEscalate}, nil)
	routeInlineSessionFn = func(_ capture.Request, _ string, _ mux.Mux) int { return 7 }
	// Fake the inline runner: drive onSubmit (so the classify goroutine runs),
	// drain it, and report a submit.
	inlineRunFn = func(_ *os.File, _ input.InlineRequest, onSubmit func(string) <-chan input.ThinkUpdate) (input.InlineResult, error) {
		ch := onSubmit("diagnose the crash")
		for range ch { // drain Line + Done
		}
		return input.InlineResult{Value: "diagnose the crash", Submitted: true}, nil
	}
	// /dev/tty may be absent in CI: only assert when the inline path is taken.
	if _, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err != nil {
		t.Skip("no /dev/tty in this environment")
	}
	if code := inlineInput(capture.Request{CWD: "/p"}, mux.Null()); code != 7 {
		t.Fatalf("inlineInput exit = %d, want 7 (escalate seam)", code)
	}
}

// With no controlling terminal and no explicit request, inlineInput exits 0
// without attempting a stdin line read (the plain-stdin read is superseded).
func TestInlineInput_NoTTYNoRequestExitsCleanly(t *testing.T) {
	if _, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		t.Skip("controlling terminal present; this asserts the no-TTY branch")
	}
	if code := inlineInput(capture.Request{CWD: "/p"}, mux.Null()); code != 0 {
		t.Fatalf("no-TTY inlineInput exit = %d, want 0", code)
	}
}
