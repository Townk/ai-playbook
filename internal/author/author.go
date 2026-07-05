package author

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/pkg/driver"
)

// maxStderrTail caps how much captured harness stderr we surface on failure.
const maxStderrTail = 2 << 10 // 2 KiB

// The harness (claude) writes routine chatter to stderr — e.g. its
// untrusted-workspace permission warnings. Piping that straight to os.Stderr
// polluted the no-mux INLINE UI (the input box renders on /dev/tty; the
// harness's stderr bled around it). So we CAPTURE the harness's stderr into a
// buffer and surface it only when the process FAILS (where it is diagnostic).
// Genuine start-time errors (e.g. "claude not found") are returned by
// exec.Start itself, so they are unaffected.

// stderrTail returns the trimmed, capped tail of captured stderr.
func stderrTail(b *bytes.Buffer) string {
	s := strings.TrimSpace(b.String())
	if len(s) > maxStderrTail {
		s = "…" + s[len(s)-maxStderrTail:]
	}
	return s
}

// withStderr annotates a non-nil process error with the captured stderr tail so
// failures stay diagnostic; on success (nil error) the captured chatter is dropped.
func withStderr(err error, b *bytes.Buffer) error {
	if err == nil {
		return nil
	}
	if tail := stderrTail(b); tail != "" {
		return fmt.Errorf("%w\n%s", err, tail)
	}
	return err
}

// Agent runs the capable agent with the given system prompt and user message and
// returns its stdout as a STREAM (io.ReadCloser) so the ui can render the produced
// playbook incrementally as the model emits it. It is injectable so tests can
// substitute a deterministic fake (no live harness). The production implementation
// is HarnessAgent (plain) / HarnessAgentWithMCP (tools-wired), both adapters over
// the config-driven RunHarnessEvents path.
type Agent func(systemPrompt, userMessage string) (io.ReadCloser, error)

// Author is the producer's LLM half: it assembles the standing system prompt and
// the per-request user message from req, then runs the agent and returns its
// stdout stream. The agent is injected (HarnessAgent in production; a fake in
// tests) so this function is deterministic to unit-test.
//
// Both knowledge sets (global + project) are loaded from disk via LoadRecall
// keyed on req.ProjectRoot and folded into the system prompt's "## What we already
// know about this project" section (global under "### About this machine and
// user", project under "### About this project"). This text path has no cfg, so
// recall uses config.Default()'s [kb] settings.
func Author(req capture.Request, agent Agent) (io.ReadCloser, error) {
	global, project := recallFor(req.ProjectRoot, nil)
	sys := SystemPrompt(req, global, project, driver.ResolveShellName(""))
	user := BuildUserMessage(req)
	return agent(sys, user)
}

// HarnessAgent is the production Agent: it runs the CONFIGURED harness via
// RunHarnessEvents and drains the model's text output into the streaming
// io.ReadCloser the callers render. opts carries the harness selection (Cfg); the
// plain (no-tools) invocation leaves MCPConfigPath empty. It replaces the removed
// claude-only ClaudeAgent so the harness is honored on every path — the legacy
// path ignored [agent].harness and always ran claude (finding A5c). The retired
// $ASSIST_MODEL/$AI_PLAYBOOK_MODEL model overrides and the bypassPermissions flag
// died with it: config ([agent].model) is now the single source.
func HarnessAgent(opts AuthorOptions) Agent {
	return func(systemPrompt, userMessage string) (io.ReadCloser, error) {
		return runHarnessText(systemPrompt, userMessage, opts)
	}
}

// runHarnessText runs RunHarnessEvents and wraps its Event channel in the streaming
// io.ReadCloser the text Agent contract promises (the model's text + a process
// error on Close). It is the shared core of HarnessAgent and HarnessAgentWithMCP.
func runHarnessText(systemPrompt, userMessage string, opts AuthorOptions) (io.ReadCloser, error) {
	events, wait, err := RunHarnessEvents(systemPrompt, userMessage, opts)
	if err != nil {
		return nil, err
	}
	return newTextStream(events, wait), nil
}

// textStream adapts a normalized agentstream.Event channel to the streaming
// io.ReadCloser the text Agent callers consume. It writes each TextDelta to a pipe
// as the model emits it (the streamed playbook), dropping Reasoning/ToolActivity
// (live-activity events the text path does not render) and Final (which DUPLICATES
// the accumulated TextDeltas for claude — see agentstream.claudeAdapter). Mirroring
// the retired procStream, Read sees a clean EOF and Close surfaces the reaped
// process error (stderr-annotated by RunHarnessEvents' wait()).
type textStream struct {
	pr      *io.PipeReader
	waitErr chan error // buffered(1): the process error, sent once the pump reaps.
}

// newTextStream starts the pump goroutine and returns the reader half.
func newTextStream(events <-chan agentstream.Event, wait func() error) *textStream {
	pr, pw := io.Pipe()
	ts := &textStream{pr: pr, waitErr: make(chan error, 1)}
	go func() {
		for ev := range events {
			if ev.Kind != agentstream.TextDelta {
				continue
			}
			if _, werr := io.WriteString(pw, ev.Text); werr != nil {
				// Reader closed early (Close before EOF). Stop writing, but keep
				// ranging so RunHarnessEvents' parse goroutine is never blocked on an
				// unread channel and wait() can still reap the process.
				for range events {
				}
				break
			}
		}
		werr := wait()
		_ = pw.Close() // EOF to the reader regardless of werr.
		ts.waitErr <- werr
	}()
	return ts
}

func (t *textStream) Read(p []byte) (int, error) { return t.pr.Read(p) }

// Close reaps the harness and returns its process error (nil on a clean exit). It
// first closes the read half so a pump blocked on a pipe write (caller closing
// before draining to EOF) unblocks and proceeds to reap.
func (t *textStream) Close() error {
	_ = t.pr.Close()
	return <-t.waitErr
}
