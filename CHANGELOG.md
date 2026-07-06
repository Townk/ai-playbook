# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Multi-harness support (`pi` and `cursor`).** The model harness is now a
  three-way choice via `[agent] harness` (`claude` | `pi` | `cursor`), each a
  self-contained in-tree adapter behind a capability contract (ADR-0012).
  - **`pi`** ships at FULL tier: structured playbook drafting
    (`submit_playbook`), the `run`/`ask` agent tools, and knowledge capture
    (`remember`) all work, carried by an embedded pi extension that dials the
    same unix-socket tools backend.
  - **`cursor`** ships at BASIC tier today: authoring runs in Cursor's
    read-only ask mode and falls back to the free-text markdown path;
    promotion to FULL is tracked.
  - **Capability tiers with visible degradation.** A BASIC harness authors
    via text mode and skips knowledge capture, and each degraded surface says
    so once per session (`structured drafting unavailable on <harness> —
    using text mode`; `knowledge capture unavailable on <harness>`) — nothing
    degrades silently.

### Changed

- The harness seam now owns its tool transport: each harness writes its own
  per-invocation transport artifact (claude an `--mcp-config` JSON, pi an
  embedded extension) instead of the launcher assuming Claude's MCP wiring.
- UI headers and the no-backend / AI-review-skipped notices name the
  **configured** harness (its display name and binary), not a hardcoded
  "Claude".
- Config defaults resolve per-harness: `model`, `triage_model`, `thinking`,
  and the binary name each fall back through the selected harness's defaults
  table (the classify-pass triage model is no longer a hardcoded Claude alias).
  Explicit `[agent]` values always win.
- Harness live tests run conditionally wherever the harness CLI is installed
  and skip otherwise (on a machine with `pi`, `make test` makes a few tiny pi
  calls).
- The cached and edit pills on the badges row now align with the subtitle
  (indented to the title text column) instead of the pane's left margin.

## [0.12.3] - 2026-07-06

### Added

- **A durable run journal.** Both run paths — the interactive viewer and
  `run --auto` — now record every playbook run to
  `<data-root>/projects/<key>/runs/<run-key>.json`: per-block outcomes
  (`ok`/`failed`/`stopped`/`rolled-back`), exit codes, durations, and
  `timed_out_after`, plus the run window, overall outcome, and the playbook's
  content hash. The journal is updated crash-safely (write-temp+rename) after
  every block result, keeps the latest run only, and is strictly advisory —
  a missing or corrupt journal never breaks `run` or `list`.
- **`run --retry` resumes the last failed run.** With no journal or a
  succeeded last run it says there is nothing to resume, and a playbook that
  changed since the failed run is refused (no partial resume of a drifted
  document). Otherwise the previously-succeeded blocks are pre-seeded as
  "done — previous run" (still manually re-runnable; `verify` is never
  pre-seeded), execution resumes at the first failed/unrun block, and any
  pre-seeded producer whose output (`from=`, `$APB_*` references) a remaining
  block consumes re-runs first — captures don't persist across sessions.
  Composes with `--auto`, `--assisted`, and `--file`.
- **A failed-run hint on plain `run`.** Running a playbook whose journal
  records a failed, stopped, or interrupted (crashed/killed mid-run) last
  run — and whose content still matches — prints one stderr line before
  starting fresh: ``last run failed at "two" (2h ago) — 'ai-playbook run
  --retry …' resumes there`` (the wording follows the outcome: `failed`,
  `stopped`, or `was interrupted`). The hint stays silent whenever `--retry`
  would just degrade to the fresh run anyway.
- **`list` shows the last run outcome.** The human format — `search` shares
  the same table — gains a LAST RUN column sourced from the current
  project's run journals: `✓`/`✗` plus the run's total elapsed time, a bare
  `✗` for a run interrupted mid-flight, or `–` when never run.

### Changed

- **Action buttons restyled as powerline pills.** The viewer's four inline
  action buttons — "try another fix", "Rollback playbook" (peach) and the
  drift row's "resolve manually", "regenerate" (blue) — now render as filled
  powerline pills (matching the header's cached/edit badges) with darker
  same-hue text; the whole pill is the click target and the activation flash
  lights the entire pill.
- **The edit badge gains an icon and full interactivity.** The `edit` pill
  now carries a pencil icon, participates in hint mode (a chip renders over
  it like every other button), flashes on activation, and keeps its full
  mouse hit box.
- **The header wraps at 80 cells.** Long titles and subtitles word-wrap at
  80 display columns; continuation lines (and the subtitle's first
  character) align under the title's first text character after the ▓▓▓
  prefix, and the layout below tracks the wrapped heights.
- **Cached/edit badges share one row.** The cached-replay pill and the edit
  pill now sit together, left-grouped, on a single badges row directly below
  the subtitle instead of occupying separate header rows.

## [0.12.2] - 2026-07-05

### Added

- **Per-block `timeout=` fence attribute.** A runnable block can now declare
  its own execution ceiling in Go duration syntax — `` ```bash {id=first-capture
  timeout=15m} `` (`90s`, `15m`, `1h`) — honored on both run paths (the viewer
  and `--auto`, including rollback targets). `validate` enforces the contract:
  an unparseable or non-positive value (`timeout=0`, negative) is an **error**
  (every block always keeps a ceiling, so unattended runs terminate), and a
  valid value on a non-runnable block (static/diff/create, where it is inert)
  is a warning. The AI author can declare it too: the structured draft schema
  carries an optional per-step `timeout` field the fence renderer emits.

### Changed

- **The default block run timeout rose from 120 seconds to 10 minutes.** The
  ceiling exists to catch hung blocks, not slow ones — two minutes killed
  legitimate long steps (installs, first backup captures). A step known to run
  even longer declares its own `timeout=`.

### Fixed

- A block run killed by its timeout now says so — `timed out after <duration>`
  (the block's declared `timeout=` or the default) in the viewer's failure
  status line and in the `--auto` step output — instead of reading as a plain
  failure.

## [0.12.1] - 2026-07-05

### Added

- **Playbook authoring quality — the rubric, taught everywhere.** A nine-rule
  authoring rubric (atomic one-step blocks, `file=` create blocks instead of
  heredocs, diff blocks for edits, a `rollback=` companion per state-mutating
  step, a final `verify` block, real `needs=`/`from=` dependencies, `{static}`
  illustration, declared `env:` + portability, danger callouts — see
  `docs/specifications/playbook-authoring.md`) is now single-sourced across
  the tool. The AI authoring prompts (both the markdown and the structured
  create paths) embed the rubric, so authored playbooks are held to it from
  the start. `ai-playbook validate` reports rubric violations as four new
  advisory **warnings** — no `{id=verify}` block, a multi-step playbook with
  zero `rollback=` blocks, a shell block writing a file via heredoc, and a
  runnable block referencing `${VAR}` undeclared in `env:` — without ever
  affecting the exit code, and its AI review pass now judges against the same
  rubric explicitly. For authoring with an external agent, the same guidance
  ships as a portable `playbook-authoring` SKILL, embedded in the binary and
  included in release archives, behind a new public `skill` verb:
  `skill show` prints the SKILL markdown to stdout, and `skill install
  [--to <dir>] [--force]` installs it (default: `~/.claude/skills/`, the
  Claude Code personal skills directory), refusing to overwrite an existing
  install without `--force`.

## [0.12.0] - 2026-07-05

### Added

- **A real, curated knowledge base** for `remember`/recall (ADR-0011, Phase 5).
  Facts now file into two sets instead of one flat per-project scratchpad: a
  **global** file (`## System` for machine/tooling truths, `## User` for who
  you are and prefer) shared across every project, and a **project** file
  (`## Environment` for this project's setup, `## Topics` for domain-specific
  lessons under `### <topic>` subsections). `remember` takes a required
  `kind` (`system`/`user`/`environment`/`topic`) that routes and classifies
  each fact, with an exact-duplicate write silently skipped (idempotent). At
  the end of a session, the wrap-up flow is now prompted to distill and
  `remember` its durable lessons before finishing, and any knowledge file that
  grows past its size budget (default 4096 bytes, `[kb] budget`) gets ONE
  compaction pass — merging near-duplicates, generalizing overlaps, dropping
  stale topics — with the prior content backed up to `knowledge.md.bak` first;
  a bad or unsafe compaction result is rejected outright and the file is left
  untouched. Recall now folds BOTH sets into every authoring-shaped call
  (initial authoring, follow-ups, final playbook/wrap-up, and drift
  regeneration), not just the first. A new public `ai-playbook kb` verb
  browses the result directly: `kb show` (both sets, or narrowed via
  `--global`/`--project <path>`), `kb edit` (opens the resolved file in
  `$EDITOR`), `kb search [--all] <query>` (case-insensitive substring search
  over facts, grouped by set/project), and `kb list` (every knowledge file's
  size and fact count).

### Fixed

- With `[kb] dir` set, facts saved via `remember` (and the end-of-session
  wrap-up fill) now land under the configured knowledge root instead of the
  default data directory. The write path was ignoring the override while recall
  honored it, so remembered facts were written where recall would never read
  them back.

## [0.11.0] - 2026-07-04

### Added

- Pipe one block's output into the next with `from=<id>` (ADR-0010). A `shell`
  or script (`run`) block tagged `{id=filter from=build}` receives the producer
  block's stdout on its **stdin** — so a Python filter reading `sys.stdin`, or a
  `cat`/`grep`/`jq` step, consumes the prior step raw, with no shell-quoting or
  size limits. `from=` implies `needs=`, so ordering, gating, and dependent
  invalidation all follow the data edge. Clicking a consumer whose producer
  hasn't run yet **materializes the chain**: each upstream producer runs first as
  an ordinary step (its own status pill and log), then the consumer — a failure
  stops the chain, and a producer that already ran this session is not re-run
  (its capture serves). `run --auto` and the guided `--assisted` walk pipe the
  same way. Each identified block now also exports **`APB_OUT_FILE_<id>`** /
  **`APB_ERR_FILE_<id>`** — the raw paths to its retained stdout/stderr — for the
  args-passing idiom `--input "$(cat "$APB_OUT_FILE_build")"`.

### Fixed

- Re-running an identified block now works under shell `noclobber` (`setopt
  noclobber` / `set -o noclobber` / `set -C`): the retained stdout/stderr capture
  is redirected with `>|`/`2>|`, so the second run overwrites its capture instead
  of failing with "file exists" and keeping the stale first-run bytes.
- `run --auto` (and the GUIDED/rollback run paths) now execute script blocks
  (python/node/ruby/perl) through their interpreter instead of feeding the raw
  program text to the shell — the assisted and rollback paths carried the
  identical raw-script bug, not a separate one, and are fixed together by this
  same change. Payload assembly moved to the schema owner
  (`pkg/playbook.ExecCommand`), the single rule both the viewer and the headless
  runner share, so a script block is written to a session temp file and invoked
  as `<interpreter> <script>` — which also frees its stdin for future `from=`
  piping.

## [0.10.0] - 2026-07-04

### Added

- The playbook schema, PTY driver, store, and dialog toolkit are now
  importable as public `pkg/` packages: `pkg/playbook` (the block/fence
  parser — the schema owner) with `pkg/playbook/frontmatter` and
  `pkg/playbook/validate`, `pkg/driver` (the unaltered-shell PTY driver),
  `pkg/store` (the playbook store scanner, now configured by explicit
  directories), and `pkg/dialog` (the themed dialog widgets behind `ask`,
  with `pkg/dialog/theme`) — the embeddable pieces of an AI-independent
  playbook toolkit (see ADR-0009; the run engine itself is still internal).
  Pre-1.0 the API may still reshape.

### Fixed

- Framed dialogs no longer leak the terminal's default background through
  unfocused rows, section labels, and gap rows — the frame background is now a
  contract every widget span honors.

## [0.9.0] - 2026-07-04

### Added

- New `ask` binary: the themed dialog widgets (confirm/line/text/choose/form)
  as a standalone tool for scripts — subcommand CLI, exit-code answers
  (0 submit/affirmative, 1 confirm-negative, 130 cancel, 2 usage/spec error),
  `ASK_*` env theming, and JSON form specs. Ships with a `man ask` page, a
  `_ask` zsh completion, and its own GoReleaser build/archive entry alongside
  `ai-playbook`/`apb`.

### Fixed

- `rollback=` authority now follows the schema rule — only top-level blocks
  count — so the renderer and `validate` can no longer disagree about which
  blocks are rollback commands.

## [0.8.1] - 2026-07-04

### Fixed

- The v0.8.0 release was blocked by its own test gate: a wall-clock assertion
  in the driver suite (`Open` < 900ms, proving the removed idle floor) is too
  strict for shared CI runners and now runs on developer machines only. No
  runtime changes since 0.8.0 — this release exists to ship the blocked one.

## [0.8.0] - 2026-07-04

### Added

- Refuse a proposed solution with a reason: any note submitted through `r`
  (refine) now persists as a session constraint injected into every later
  regeneration/follow-up — and a note that rejects the current approach makes
  the agent re-author from scratch instead of patching it. Active constraints
  show in the status line.

### Changed

- Opening a session no longer pays a fixed ~1.2s settle delay — readiness is
  probed immediately. Each run's sentinel now carries a per-run random nonce, so
  a stale sentinel left over from an earlier run (or a probe swallowed during
  shell init that prints late) can never satisfy a later run's wait, making
  stale-output collisions impossible.
- Opening a playbook with diff blocks no longer queues per-block drift checks
  behind the session shell — they run instantly and never contend with an
  in-flight block.
- A running block's spinner no longer re-renders the whole document 10×/second;
  text-input fields stop rebuilding their textarea styles every frame.

## [0.7.0] - 2026-07-03

### Changed

- Large playbooks and diffs render smoothly during runs and scrolling: the
  render pipeline now reuses a single markdown parser, memoizes syntax
  highlighting of unchanged code blocks, and caches the scroll width and diff
  overlay geometry, so spinner/stream ticks and keypresses no longer re-run the
  whole pipeline.
- Authoring prompts send the request context once: the failed command,
  captured scrollback, and the user's request used to be interpolated into
  both the standing system prompt and the per-request user message, paying
  their token cost twice on every authoring/followup/final call. That
  context now travels only in the user message.
- Running a stored playbook by slug now resolves its working directory by
  the same rule as `run --file`: a non-project_bound stored playbook opens
  in its own file's directory (the store's content dir) instead of the
  invocation cwd; `workdir:` front matter still overrides either way.

### Removed

- The environment overrides read only by the retired legacy Claude invocation
  path: `$ASSIST_MODEL` / `$AI_PLAYBOOK_MODEL` (authoring model — now solely
  `[agent].model`), `$AI_PLAYBOOK_CLAUDE_BIN` (harness executable — now solely
  `[agent].bin`), and `$AI_PLAYBOOK_CLAUDE_PERMISSION_MODE`. Its
  `--permission-mode bypassPermissions` flag is dropped with it — the unified
  invocation passes no permission mode.

### Fixed

- A stalled model backend no longer doubles the `assist` triage wait: the
  classify pass skips its one-retry when the first attempt timed out (retrying
  a hung harness could only time out again, turning a 60s worst case into 120s).
- A model-submitted playbook with a backtick in a block `id` or `file=` value is
  now rejected at submit time (and the model asked to correct it): a backtick in
  the rendered fence info string is CommonMark-invalid and would end the fence
  early, corrupting the rendered document.
- A duplicated session teardown is now a true no-op: closing the shell session
  twice used to re-signal the already-reaped shell's process group (a pid the OS
  may have handed to an unrelated process by then) and re-probe its released
  terminal descriptor (whose number may likewise be reused).
- A corrupted or truncated Claude output stream is now reported as an error
  instead of silently passing for success: the stream parser rejects a non-JSON
  line (a stream cut mid-line, or a configured `[agent] bin` that is not
  actually Claude) and a stream that ends without Claude's terminal result
  record — previously both were skipped, so a clean process exit could deliver
  an empty or partial playbook with no indication anything went wrong.
- Keyboard hint-mode activation of an assisted-run footer button (Run / Skip /
  Roll back / Leave as-is / Quit) now works: selecting one via the Space-leader
  hint labels used to be a silent no-op — only a mouse click dispatched it. Both
  input paths now share one button dispatcher, so they can no longer drift apart.
- The configured harness (`[agent].harness`) is now honored on the re-engagement
  and fallback authoring paths: those used to run Claude unconditionally, ignoring
  a non-Claude harness selection. All agent calls now route through the single,
  config-driven harness invocation path.
- Relative file paths (create-file, view/apply-diff targets, the diff float)
  now resolve against the session's live working directory: a `cd` inside a run
  block is tracked, instead of everything resolving against the stale
  directory the session started in.
- Quitting while a block is still running no longer hangs: tearing the session
  down now interrupts the in-flight run promptly instead of waiting out its full
  timeout, and it tears down the running command's process group so no orphaned
  child processes are left behind.
- Rapidly triggering create-file and its undo (two quick clicks) no longer risks
  a crash: the file-backup bookkeeping shared by those actions is now guarded
  against the concurrent access the UI's action goroutines could produce.
- `run <slug>` (a stored playbook) now renders through the same code path as
  `run --file <path>`: its declared `env:` map (the confirmation gate),
  description subtitle, and `project_root` are no longer silently dropped, and
  the run no longer leaks a temp file per invocation.
- A `depends_on` chain's `PROJECT_ROOT` no longer leaks across nodes: a
  project-bound dependency's resolved root used to persist via a process-wide
  `os.Setenv` and bleed into a later non-bound dependency and the parent's own
  driver; it is now scoped to that dependency's run only.
- A troubleshoot request classified as a `command` no longer vanishes silently
  when staging it into the origin pane fails (e.g. the pane is gone); the
  suggested command is now printed to stderr instead.
- Assist summoned via `apb` no longer captures its own invocation as the last
  command.
- Paste now works in form fields and the choose dialog's "other" entry.
- `diff` parsing no longer mistakes a deleted/added line whose own content
  starts with `-- `/`++ ` (e.g. an SQL comment) for a new file header, which
  previously truncated or misattributed the rest of the hunk.
- Tab-indented diffs no longer overflow their side-by-side cell or drift the
  `│` divider off-column; tabs are expanded to spaces before any width
  calculation, in both the `diff` CLI view and the in-UI diff overlay.
- Drift conflict markup no longer silently drops a hunk whose leading context
  also occurs earlier in the file above a prior hunk's region — each hunk now
  anchors in file order instead of always searching from the top.
- A submitted code block whose payload contains its own run of 3+ backticks
  (e.g. an embedded markdown/shell example) no longer closes the rendered
  fence early — the fence now widens to stay longer than the longest
  backtick run in the payload.
- Submit-time playbook validation now catches a dangling `needs=`/`rollback=`
  reference, a `needs=` cycle, and an id or `file=` value containing
  whitespace/`{`/`}`/`=` (any of which would corrupt the rendered fence tag)
  — previously these only surfaced later, on the post-hoc `validate` pass.
- Env-var references written as parameter expansions (`${FOO:-default}`,
  `${BAZ%.*}`, `${BAZ#prefix}`, `${VAR/a/b}`) are now captured into the
  saved playbook's `env:` front matter; previously only the bare `${VAR}`
  form was recognized and these were silently omitted.
- A malformed or truncated stream from the AI harness is no longer mistaken
  for success: a parse failure on the harness's stdout is now surfaced (joined
  with the process's exit error) instead of being silently discarded.
- The classify and metadata calls (the quick triage/JSON decisions that gate
  every request) no longer hang indefinitely against a stalled harness
  process — they are now bounded by a generous default timeout, after which
  the process is killed and a clear timeout error is returned.

## [0.6.1] - 2026-07-03

### Fixed

- `go install`-ed binaries now report their real module version (read from the
  embedded build info) instead of `dev`. Release-archive builds are unchanged —
  they still carry the version injected at build time.

## [0.6.0] - 2026-07-03

### Added

- Comprehensive `--help`: a grouped top-level overview and real per-command
  help via `ai-playbook <command> --help` / `help <command>`, generated man
  pages, and a zsh completion script with dynamic completion of saved
  playbook slugs — all packaged in the release archives.
- `apb`, a short-name binary built from the same code as `ai-playbook`:
  install it directly (`go install .../cmd/apb`) or grab it from any release
  archive, where it now ships alongside `ai-playbook`. Both binaries behave
  identically; `--help`/`help` and `--version` are name-aware (`apb --help`
  reads "apb").

### Changed

- The confirm dialog's button row is now horizontally centered within the pane
  (previously left-aligned).

## [0.5.0] - 2026-07-03

### Added

- Live playbook store (Phase 1): saved playbooks are now a browsable, searchable,
  editable, re-runnable library. New commands — `list`/`search`
  (`--format human|fuzzy-data-source|json`), `show`, `edit`, and `create` (author a
  playbook directly). `troubleshoot` is renamed to `assist` (the old name still works).
  A global store plus a project-local store (`.ai-playbook/playbooks/`, `proj:`-prefixed
  slugs); both directories are configurable via `[store]`. `run <slug>` adapts a stored
  playbook to the current project (with an "adapted from" banner and a `d` diff view);
  `run --file <path>` runs a file directly. Playbooks gain a `workdir` front-matter field.
- Runs without a terminal multiplexer (ADR-0006 Stage 1): off-mux, the input box
  renders inline below the shell prompt, and the agent's `ask` dialog renders as an
  in-viewer overlay (all types: text/line/confirm/choose/free). With a multiplexer
  present, the floating-pane experience is unchanged.
- Configurable shell (ADR-0006 Stage 2): `[driver] shell` selects the executing
  shell — `zsh`, `bash`, or POSIX `sh`. bash and sh are supported with per-shell
  value-passing that round-trips special characters; zsh gives full fidelity
  (aliases/functions/rc). The default honors `$SHELL` (see *Changed*).
- **Run modes for `run`** — `--assisted` (guided: a "ready" cursor auto-scrolls
  each next step into view; a focusable `[ Run ][ Skip ][ Quit ]` footer confirms
  each step; on failure it switches to `[ Roll back ][ Leave as-is ][ Quit ]`) and
  `--auto` (headless: runs every block in `needs=` order, stops on the first
  failure with a non-zero exit and a summary; renders inline in the terminal /
  CI-friendly). `--auto` rolls back completed steps in reverse order on failure by
  default (via `{rollback=<id>}` blocks); `--no-auto-rollback` opts out. Each run
  writes a structured JSON log under `${XDG_DATA_HOME}/ai-playbook/runs/`.
- **`validate [<slug> | --file <path>]`** — deterministic structural checks
  (front-matter required keys, `needs=` existence and cycles, duplicate ids, fence
  balance; plus no-runnable / missing-language warnings) and an advisory AI prose
  review, with live progress (a spinner on a TTY, a dot heartbeat in CI) and
  `--no-ai` / `--plain` / `--quiet`. Exits non-zero on structural errors only.
- **Viewer affordances** — an `[edit]` tag-button opens `$EDITOR` on a file-backed
  playbook and the viewer reloads on save (1s mtime watch); a pure-Go side-by-side,
  syntax-highlighted diff view (ADR-0008) backs both the `diff`-block "view diff"
  button and the adapt-on-run `d` overlay, mux-aware (a floating pane with a
  multiplexer, a modal overlay without).
- **`run --auto --with-env <JSON | file>`** — supply a project-bound playbook's
  declared `env:` values on the CLI as an inline JSON object or a path to a JSON
  file, instead of exporting them. Values take precedence over the environment;
  undeclared keys are ignored with a warning. Valid only with `--auto`.
- **`env [<slug> | --file <path>]`** — print a playbook's declared `env:` as a
  `--with-env`-compatible JSON object, each value resolved from the current
  environment (sensitive values — token/key/secret/password-like names or
  high-entropy values — are emitted empty and listed on stderr). Scaffolds the
  round-trip `env > env.json` → edit → `run --auto --with-env env.json`.
- **`depends_on: [slug, …]`** front-matter field — a playbook can declare other
  store slugs it needs run first. `run <slug>` resolves the transitive
  dependency graph and runs each dependency headless, in topological order,
  before the parent; the first failure aborts the whole chain with a non-zero
  exit. A dependency cycle or a dangling (unresolvable) slug is a hard error
  (exit 2); `validate` flags the same issues as structural errors. `--with-env`
  and `env <slug>` both span the entire chain — the union of every variable
  declared anywhere in the graph.

### Changed

- `create <prompt>` now shows **inline progress** while authoring — the spinner +
  `Waiting…` + elapsed + model-activity line render below the shell prompt (not the
  fullscreen viewer) — and only then opens the viewer with the **complete** playbook
  (no live-stream takeover). The flow is identical with or without a multiplexer, and
  the authoring agent's `ask` is supported throughout (float with a mux; an inline ask
  box, paused/resumed around the progress line, without one).
- Authored playbooks now target the configured shell: `sh` runs receive POSIX-only
  guidance (no `[[ ]]`, arrays, or bash/zsh extensions); `bash` and `zsh` runs are
  identified explicitly. The effective shell is resolved from `[driver] shell` (or
  `$SHELL` when unset) and injected into the authoring prompt.
- The multiplexer integration is now **OFF by default** (was: auto-enabled inside
  zellij). Opt in with `[mux] backend = "zellij"`. The `$ZELLIJ`-presence
  auto-enable is removed; per-command `[mux]` template overrides remain as tier-2.
  **Behavior change** (ADR-0007): pre-existing users who relied on auto-zellij must
  add `[mux] backend = "zellij"`.
- The shell driver now **defaults to `$SHELL`** (was: zsh-first). With no
  `[driver] shell` set it honors the login shell when its basename names a supported
  shell (zsh/bash/sh), falling back `zsh` → `bash` → `sh`. Pin a specific shell with
  `[driver] shell`. **Behavior change** (ADR-0007); a zsh user is unaffected.
- **Run-block value-passing env vars renamed** `AAS_*` → `APB_*`: the exported
  variables are now `APB_OUT_<id>`, `APB_ERR_<id>`, and `APB_EXIT_<id>` (were
  `AAS_OUT_<id>`, `AAS_ERR_<id>`, `AAS_EXIT_<id>`). The old prefix was a leftover
  from the retired "ai-assist" shell stack. If you have saved playbooks that reference
  the old names, update them: `s/\$AAS_/\$APB_/g`. The store is days old so few if
  any saved playbooks should be affected.

### Fixed

- Framed dialogs are now fully painted on their background — no bleed-through of
  the terminal's default: the variable-confirmation dialog's prompt body, button
  row (now `[ Confirm ][ Customize ][ Quit ]`), and hint line; the `choose`
  dialog's prompt; and the text-input box interior. The hint line sits flush
  against the bottom border (no trailing blank line), and the confirmation
  dialog's variables render in an aligned two-column layout with long values
  wrapping under the value column.
- In the confirm gate, **ESC** and the new **Quit** button end the run; ESC while
  editing a variable (Customize) steps back to the confirm dialog instead of
  quitting.
- ESC during the `assist` classify wait now cancels the request instead of
  proceeding to route it as if submitted.
- `--assisted` now confirms a project-bound playbook's declared variables at load
  (before the first step), matching the run-modes spec.

## [0.3.0] - 2026-06-26

### Added

- Single Go binary unifying and replacing the retired shell-script stack;
  harness-agnostic design (Claude harness today), invoked directly or bound to a
  shell key.
- `assist` triage (command / answer / escalate) with routing.
- Cache-by-kind: a repeat command/answer/playbook is served without re-classify;
  a cached answer invalidates in place (reload re-runs the cheap classify).
- In-process re-engagement: regenerate / follow-up / wrap-up.
- Auto-follow-up on a failed verify; native verify-success confirm (green
  ask-style buttons, `c` to generate).
- The wave thinking animation.
- Replace-protection: never persist a non-playbook over the resolved troubleshoot.
- Front matter (`name`/`description`/`category`/`tags`/`env`) with `finalize`
  backfill.
- Multi-language run blocks (shell plus python/node/ruby/perl via interpreter
  heredocs).
- MCP tools backend (run / ask / remember) over a unix socket, dialing the shared
  shell driver.

### Changed

- Performance: classify runs thinking-OFF (~2.6s vs ~7–9s); async session open so
  cached playbooks render instantly and shell buttons enable when ready; answers
  skip the driver.
- Rebrand: environment variables renamed to `AI_PLAYBOOK_*`; `ai-playbook` labels
  and cache schema; corrected system-prompt tool references (MCP run/ask/remember).

### Removed

- The retired zsh + `libexec/` shell stack.
- Dead FIFO plumbing, including `--results-fifo` and the broker process.

## [0.2.0] - (historical)

- First all-Go-binary release (replaced the shell stack).

## [0.1.0] - (historical)

- Original zsh shell-script implementation (ai-assist).

[Unreleased]: https://github.com/Townk/ai-playbook/compare/v0.12.3...HEAD
[0.12.3]: https://github.com/Townk/ai-playbook/compare/v0.12.2...v0.12.3
[0.12.2]: https://github.com/Townk/ai-playbook/compare/v0.12.1...v0.12.2
[0.12.1]: https://github.com/Townk/ai-playbook/compare/v0.12.0...v0.12.1
[0.12.0]: https://github.com/Townk/ai-playbook/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/Townk/ai-playbook/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/Townk/ai-playbook/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/Townk/ai-playbook/compare/v0.8.1...v0.9.0
[0.8.1]: https://github.com/Townk/ai-playbook/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/Townk/ai-playbook/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/Townk/ai-playbook/compare/v0.6.1...v0.7.0
[0.6.1]: https://github.com/Townk/ai-playbook/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/Townk/ai-playbook/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/Townk/ai-playbook/compare/v0.3.0...v0.5.0
[0.3.0]: https://github.com/Townk/ai-playbook/releases/tag/v0.3.0
[0.2.0]: https://github.com/Townk/ai-playbook/releases/tag/v0.2.0
[0.1.0]: https://github.com/Townk/ai-playbook/releases/tag/v0.1.0
