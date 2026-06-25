package main

import (
	"bytes"
	"io"

	"ai-playbook/agentstream"
)

// fanout splits a normalized agentstream.Event channel into the two surfaces the
// ui already consumes: an io.ReadCloser carrying the playbook markdown (fed into
// ui.RunStream's reader-based stream) and an activity chan string carrying the
// model's live reasoning + tool summaries (the ui's "⟳ …" activity line). It also
// accumulates the playbook BODY for the cache.
//
// Event mapping (the part-1 contract — see package agentstream):
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
// On the event channel closing: the playbook pipe is closed (EOF to the reader),
// the activity channel is closed (signals the ui to stop subscribing), and closeFn
// is called to reap the harness process. The pump runs in a goroutine and never
// blocks the harness — activity sends are best-effort (drop-if-full) so a slow ui
// can't stall the pipe writes.
//
// The returned reader is consumed by the ui; Body() yields the accumulated cache
// body and is valid after the reader hits EOF.
type fanout struct {
	body bytes.Buffer
}

// Body returns the accumulated playbook body for the cache. It is complete once
// the returned reader has reached EOF (the event channel drained).
func (f *fanout) Body() string { return f.body.String() }

// fanOut starts the pump and returns the playbook reader, the activity feed, and a
// *fanout whose Body() holds the cache text after EOF. activityBuf bounds the
// activity channel (drop-if-full on send).
func fanOut(events <-chan agentstream.Event, closeFn func() error, activityBuf int) (io.ReadCloser, <-chan string, *fanout) {
	pr, pw := io.Pipe()
	activity := make(chan string, activityBuf)
	f := &fanout{}

	go func() {
		var (
			wroteDelta bool
			finalText  string
			haveFinal  bool
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
			case agentstream.TextDelta:
				// Write to the playbook pipe and accumulate the body. A pipe write
				// error means the reader went away; keep draining so closeFn still runs.
				_, _ = io.WriteString(pw, ev.Text)
				f.body.WriteString(ev.Text)
				wroteDelta = true
			case agentstream.Final:
				finalText = ev.Text
				haveFinal = true
			case agentstream.Reasoning, agentstream.ToolActivity:
				sendActivity(ev.Text)
			}
		}

		// Final handling: a Final-only stream (no deltas) must still render — write
		// the Final text to the pipe and use it as the body. When deltas streamed,
		// the pipe already carries the playbook; Final is authoritative for the
		// stored body only (prefer it when present).
		if haveFinal {
			if !wroteDelta {
				_, _ = io.WriteString(pw, finalText)
			}
			f.body.Reset()
			f.body.WriteString(finalText)
		}

		_ = pw.Close()
		close(activity)
		if closeFn != nil {
			_ = closeFn()
		}
	}()

	return pr, activity, f
}
