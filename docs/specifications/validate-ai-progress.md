# `validate` AI-review progress feedback (spec)

Status: proposed (2026-07-01). Enhancement to the just-shipped `ai-playbook validate`
(`docs/specifications/validate-command.md`): its Layer-2 AI prose review currently blocks
silently, unlike `create` which shows a live spinner + model activity.

## Goal

While `validate`'s AI review runs, show progress:

- **Interactive (TTY):** the same inline **spinner + `Waiting…` + elapsed + model-activity
  line** that `create` shows — by reusing `runCreateProgress`.
- **CI / non-TTY (a discreet second mode):** a **`.` printed to stderr every 2s** while the
  review runs (a heartbeat so a CI job/log sees liveness and doesn't look hung), then a
  trailing newline.
- **`--no-ai`:** no AI pass, so no progress.

Progress never touches **stdout** — the spinner renders on `/dev/tty`, the heartbeat dots go
to **stderr**, so `validate … > report.txt` keeps a clean report.

## Background (grounded)

- `validate`'s AI pass calls `author.ReviewOnce` (`internal/author/review.go`) →
  `runMetadataOnce` (`internal/author/metadata.go:113`), which **drains the harness stream
  internally and returns the finished text** — no event feed is exposed, so there is nothing
  to animate.
- `create` shows progress via the **streaming** path: `author.RunHarnessEvents(sys, user,
  opts) (<-chan agentstream.Event, func() error, error)` (`internal/author/events.go:181`) →
  `agentstream.FanOut(events, closeFn, ActivityBuffer) (io.ReadCloser, <-chan string, *Fan)`
  (`internal/agentstream/fanout.go:48`) → `runCreateProgress(activity, bridge, done)`
  (`internal/launcher/create_progress.go:171`), a bubbletea host on `/dev/tty` that renders
  spinner + activity and, **when there is no `/dev/tty`, just waits for `done`** (silent).
- `runCreateProgress` lives in `internal/launcher` — the **same package** as
  `validatecmd.go` — and is already behind the `runCreateProgressFn` seam. `validate` can
  call it directly.

## Design

1. **`internal/author`:** replace the blocking `ReviewOnce` with a streaming
   `ReviewStream(cfg *config.Config, systemPrompt, userMessage string) (<-chan
   agentstream.Event, func() error, error)` — the streaming sibling built on
   `RunHarnessEvents` with the same review options (`Bare=true`, `MCPConfigPath=""`,
   `Cfg=cfg`) but `NoThinking=false` so the activity feed has model activity to show. Remove
   `ReviewOnce` (only `validate` used it) + its tests; port the relevant assertions to
   `ReviewStream` (event stream carries the review text; error propagates for no-backend
   detection).
2. **`internal/launcher/validatecmd.go`** — rework the AI pass (only when not `--no-ai`):
   - `events, closeFn, err := reviewStreamFn(cfg, reviewSystemPrompt, body)`. On error →
     the existing no-backend/failed skip note (no progress). (`var reviewStreamFn =
     author.ReviewStream` seam replaces `reviewFn`.)
   - `reader, activity, _ := agentstream.FanOut(events, closeFn, ActivityBuffer)`.
   - Drain `reader` on a goroutine → the review text; `close(done)` at EOF.
   - **Progress mode by TTY** (a `hasTTY()` helper: `os.OpenFile("/dev/tty", …)` ok?):
     - TTY → `runCreateProgressFn(activity, nil, done)` (spinner + activity; bridge nil — a
       review needs no ask).
     - no TTY → a goroutine drains+discards `activity` (so `FanOut` never blocks), and a
       heartbeat loop prints `"."` to **stderr** every 2s until `done`, then `"\n"`.
   - `<-done`; then print the captured review text in the `AI review:` block (unchanged
     report format).
   - **Draining `activity` is mandatory** in the no-TTY path — an unread buffered activity
     channel would stall the fan-out and thus the reader drain (→ `done` never closes).
3. Report to **stdout** unchanged; exit code still driven only by structural errors.

## Testing

- **`author.ReviewStream`:** via the existing harness seam (as `ReviewOnce`'s tests did) —
  the returned event stream yields the review text; a no-backend start error propagates.
- **`validatecmd`:** stub `reviewStreamFn` to return a fake event stream (a closed/canned
  `agentstream.Event` channel) + stub `runCreateProgressFn` (already a seam) so no TTY/model
  is needed; assert the AI text is captured + printed, and that `--no-ai` skips the stream.
  For the CI heartbeat: factor the heartbeat into a small testable unit (e.g. a
  `heartbeat(w io.Writer, done <-chan struct{}, every time.Duration)` that writes a `.` per
  tick until `done`) — test it writes ≥1 dot to a buffer before `done` closes and a trailing
  newline after. (The TTY-vs-noTTY branch selection itself is thin; the heartbeat unit +
  the `runCreateProgressFn` seam cover both arms without a real terminal.)

## Out of scope

- An explicit flag to force a progress mode — selection is automatic by TTY presence (CI is
  the non-TTY case). `--no-ai` already covers "no AI / no progress".
- Any change to `create`'s progress host (`runCreateProgress` is reused as-is).
- Streaming/rendering the review text live — it's drained and printed once at the end (the
  activity line, not the review body, is the live feedback).
