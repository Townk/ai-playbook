package agentstream

import (
	"bytes"
	"io"
)

// FanOut splits a normalized Event channel into the two surfaces the ui consumes:
// an io.ReadCloser carrying the playbook markdown (fed into the ui's reader-based
// stream) and an activity chan string carrying the model's live reasoning + tool
// summaries (the ui's "⟳ …" activity line). It also accumulates the playbook BODY
// for the cache. This is the shared core used by BOTH the initial authoring path
// (package main) and re-engagement (package orchestrator) so they render the
// model's live reasoning identically.
//
// Event mapping (the part-1 contract):
//
//   - TextDelta    → written to the playbook pipe AND appended to the body buffer.
//   - Final        → if NO TextDelta was ever written, the Final text is written to
//     the pipe (so a harness that only emits a final result still renders) and set
//     as the body; otherwise Final is authoritative for the stored body only
//     (prefer Final's text for the cache when present), leaving the streamed pipe
//     content untouched.
//   - Reasoning    → sent to the activity channel (the live model reasoning).
//   - ToolActivity → sent to the activity channel (the tool summary).
//
// On the event channel closing: the body is written to the playbook pipe, closeFn
// is called to reap the harness process AND observe its outcome, then the pipe is
// closed — WITH closeFn's error when it failed (stream truncation, timeout kill,
// non-zero exit), so the reader sees the failure instead of a clean EOF — and the
// activity channel is closed (signals the ui to stop subscribing). The pump runs
// in a goroutine and never blocks the harness — activity sends are best-effort
// (drop-if-full) so a slow ui can't stall the pipe writes.
//
// The returned reader is consumed by the ui; (*Fan).Body() yields the accumulated
// cache body and is valid after the reader hits EOF.

// Fan exposes the accumulated playbook body after the fan-out reader reaches EOF.
type Fan struct {
	body bytes.Buffer
}

// Body returns the accumulated playbook body for the cache. It is complete once
// the returned reader has reached EOF (the event channel drained).
func (f *Fan) Body() string { return f.body.String() }

// FanOut starts the pump and returns the playbook reader, the activity feed, and a
// *Fan whose Body() holds the cache text after EOF. activityBuf bounds the activity
// channel (drop-if-full on send).
func FanOut(events <-chan Event, closeFn func() error, activityBuf int) (io.ReadCloser, <-chan string, *Fan) {
	pr, pw := io.Pipe()
	activity := make(chan string, activityBuf)
	f := &Fan{}

	go func() {
		var (
			deltaBuf  bytes.Buffer // fallback body if no Final is emitted
			finalText string
			haveFinal bool
		)
		sendActivity := func(s string) {
			// Best-effort, never block the pump: a slow ui drops the summary.
			select {
			case activity <- s:
			default:
			}
		}
		for ev := range events {
			switch ev.Kind {
			case TextDelta:
				// Streamed text is the agent's live PROCESS output — interim narration
				// ("I will verify the state…") AND the answer being written — NOT the
				// rendered doc. Surface it transiently on the activity line (like tools /
				// reasoning) so the spinner+activity show the whole wait; the doc pipe
				// stays empty until the authoritative result arrives, so `thinking` (and
				// the spinner) survive the entire authoring phase. Accumulate as a
				// fallback body in case a harness emits no Final.
				sendActivity(ev.Text)
				deltaBuf.WriteString(ev.Text)
			case Final:
				finalText = ev.Text
				haveFinal = true
			case Reasoning, ToolActivity:
				sendActivity(ev.Text)
			}
		}

		// The doc is written ONCE, at completion, from the authoritative final answer
		// (claude `result` = the final message text = the playbook, excluding interim
		// narration). Fall back to the accumulated deltas only if no Final was emitted.
		body := finalText
		if !haveFinal {
			body = deltaBuf.String()
		}
		_, _ = io.WriteString(pw, body)
		f.body.Reset()
		f.body.WriteString(body)

		// A5a-full: surface the harness outcome on the READER. closeFn (the
		// producer's wait) reports a strict-adapter stream truncation, a
		// timeout kill, or a non-zero exit; discarding it made a truncated
		// authoring stream indistinguishable from success (A5b-strict only
		// protected triage). The body is written first, so a consumer still
		// renders the partial text before hitting the error on the final Read.
		var werr error
		if closeFn != nil {
			werr = closeFn()
		}
		if werr != nil {
			_ = pw.CloseWithError(werr)
		} else {
			_ = pw.Close()
		}
		close(activity)
	}()

	return pr, activity, f
}
