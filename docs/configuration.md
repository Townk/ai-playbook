# Configuration

ai-playbook works with **no config file present** — every setting has a baked-in
default. You configure it two ways: a TOML config file (persistent preferences) and
`AI_PLAYBOOK_*` environment variables (overrides, paths, and a few internal knobs).

## Config file

Location: `$XDG_CONFIG_HOME/ai-playbook/config.toml` (fallback
`~/.config/ai-playbook/config.toml`). Only the keys you set override the defaults;
everything else falls through to the baked-in profile. A missing file is fine; a
malformed file is a loud error.

### `[agent]`

Selects the model harness and a few value preferences. The harness *invocation*
(flags, stream parser, tool transport) is owned in-tree — you only pick which
harness plus these values.

| Key            | Default    | Meaning |
|----------------|------------|---------|
| `harness`      | `claude`   | Which shipped harness to drive: `claude`, `pi`, or `cursor`. An unknown name is a loud error, never a silent fallback. |
| `model`        | `""`       | Model id passed to the harness for authoring; empty → the harness's own default (table below). |
| `triage_model` | `""`       | Model id for the cheap one-shot CLASSIFY pass (command/answer/escalate); empty → the harness's own default (table below). |
| `bin`          | `""`       | Override for the harness executable path; empty → the harness's default binary resolved on `PATH` (table below). |
| `thinking`     | `""`       | Reasoning effort: `off` / `low` / `medium` / `high`. On claude it maps to a `MAX_THINKING_TOKENS` budget (`medium` ≈ 8000 tokens; a bare integer is accepted as a raw budget); on pi it maps to the native `--thinking` flag (which also accepts pi's own `minimal` / `xhigh`); cursor has no thinking lever, so the value is ignored there. Empty → the harness's own default (table below). |

**Per-harness defaults.** An empty value resolves through the selected
harness's defaults row; an explicit value always wins:

| Harness  | `model`               | `triage_model`        | `thinking` | binary (`bin`) |
|----------|-----------------------|-----------------------|------------|----------------|
| `claude` | harness default       | `haiku`               | `medium`   | `claude`       |
| `pi`     | your pi default model | your pi default model | `medium`   | `pi`           |
| `cursor` | cursor's default      | cursor's default      | none       | `cursor-agent` |

pi and cursor are multi-provider/plan-scoped — any concrete cheap triage model
baked in as a default could name one *you* cannot call, so their triage runs on
your own default model; set `triage_model` to pick a cheaper one. Cursor's CLI
installs as `agent`/`cursor-agent` (never `cursor`), so its default binary is
the every-vintage `cursor-agent` name; set `bin = "agent"` if you prefer the
primary name.

**Capability tiers.** claude and pi are FULL harnesses: authoring drafts
structured playbooks through `submit_playbook`, the agent probes with the
`run`/`ask` tools, and session lessons are captured into the knowledge base via
`remember`. cursor ships BASIC today (read-only ask-mode invocations):
authoring falls back to the free-text markdown path, and each degraded surface
says so once per session — `structured drafting unavailable on Cursor — using
text mode`, and `knowledge capture unavailable on Cursor` when a wrap-up would
have filled the knowledge base. Nothing degrades silently: the notes are the
signal.

**Contributor note:** the harness live tests run wherever the harness CLI is
installed and skip elsewhere — on a machine with pi installed, `make test`
makes ~3 tiny pi calls billed to your pi subscription.

### `[driver]`

Selects the executing shell for run-blocks.

| Key     | Default        | Meaning |
|---------|----------------|---------|
| `shell` | `""` (auto)    | The shell preset to spawn: `zsh`, `bash`, or POSIX `sh`. The default `""` means **auto** — honor `$SHELL` when its basename names a supported shell and it resolves, otherwise fall back `zsh` → `bash` → `sh`. A `zsh` login shell is therefore unaffected. Set an explicit value to pin one regardless of `$SHELL`. |

### `[mux]`

The terminal multiplexer is **OFF by default**: with no `[mux] backend` set,
ai-playbook uses the inline (no-mux) UX even inside zellij. Opt in with a tier-1
selector — `backend = "zellij"` — that mirrors `[driver] shell` and
`[agent] harness`.

| Key       | Default     | Meaning |
|-----------|-------------|---------|
| `backend` | `""` (off)  | The named multiplexer preset to enable. `""` = off (inline UX). `"zellij"` = the built-in zellij preset (the command templates below). Any other value requires full per-command template overrides. |
| `pane-id` | `terminal_{ZELLIJ_PANE_ID}` | The **origin-pane identity** template: how the pane id of the shell a request originates from is derived from that shell's environment. Each `{VAR}` expands to the env var's value; if **any** referenced var is unset the id resolves to `""` (no origin pane — capture falls back to the focused pane, and the play button degrades to the clipboard with a status note). The resolved id feeds `dump-screen` and `type-into-pane` via their `{pane}` placeholder. tmux users set `pane-id = "{TMUX_PANE}"` (tmux `%`-ids are used verbatim; zellij needs the `terminal_` prefix on its numeric env id). |

When the mux is enabled, each action is driven by a command **template** (tier-2,
for fine-grained control). Each value is a template string; the binary token-splits
it, substitutes placeholders (`{cmd}`, `{cwd}`/`{cwdarg}`, `{pane}`/`{panearg}`,
`{width}`, `{height}`, `{name}`/`{namearg}`, `{text}`, `{title}`), and runs the
resulting argv directly (it is **not** a shell). The defaults are the zellij preset;
override one only to target a different multiplexer or tweak a single action.

| Key                  | Action |
|----------------------|--------|
| `open-floating-pane` | Spawn a floating pane (the thinking/working float). |
| `open-input-float`   | Spawn the borderless, pinned request/ask input float. |
| `open-docked-pane`   | Spawn a docked pane (to the right). |
| `dump-screen`        | Capture a pane's scrollback (for context capture). |
| `type-into-pane`     | Write characters into the origin pane (targets `--pane-id`). |

The template defaults are the zellij commands from `config.Default()`; see
`config/config.go` for the exact strings and the placeholder/argv-safety contract.
An empty template value after merge means that action is unconfigured.

### `[kb]`

Controls the two-set knowledge base (ADR-0011): the GLOBAL file
(`## System`/`## User`, shared across projects) and each project's file
(`## Environment`/`## Topics`) that `remember`/`recall` and the `kb` CLI verb
read and write.

| Key      | Default | Meaning |
|----------|---------|---------|
| `budget` | `4096`  | Per-file size budget in bytes. A knowledge file over budget gets ONE compaction pass at solution completion (merge/generalize/drop-stale, `.bak` written first); recall's hard read-time tail-cap is a multiple of this value. |
| `dir`    | `""`    | Root override for the knowledge files. Empty (the default) derives the root from the shared data dir (`AI_PLAYBOOK_DATA_DIR`, else the XDG data home). A `~`/`~/` prefix is home-expanded. |

Example:

```toml
[agent]
harness = "claude"   # or "pi" / "cursor"; empty values below resolve per-harness
model = "sonnet"
triage_model = "haiku"
thinking = "medium"

[driver]
shell = "bash"   # pin bash; omit (or "") to auto-honor $SHELL

[mux]
backend = "zellij"   # opt in to the multiplexer (off by default)
# tier-2: override a single action only if you are not on the default zellij setup
dump-screen = "tmux capture-pane -p {panearg}"
# origin-pane identity for a tmux setup ({VAR} expands from the origin shell's env)
# pane-id = "{TMUX_PANE}"

[kb]
budget = 4096   # per-file byte budget before wrap-up compaction kicks in
dir = ""        # "" derives from the shared data dir; set to override
```

## Environment variables

All ai-playbook env vars are prefixed `AI_PLAYBOOK_`.

### User-facing

| Variable                  | Default              | Purpose |
|---------------------------|----------------------|---------|
| `AI_PLAYBOOK_DATA_DIR`    | `${XDG_DATA_HOME:-~/.local/share}/ai-playbook` | The data dir: the playbook store + the response cache (see [storage](specifications/storage.md)). Highest-priority override. |
| `AI_PLAYBOOK_NO_CACHE`    | unset (cache on)     | When set (non-empty), disable cache serving (always escalate/author). |
| `AI_PLAYBOOK_PROJECT_ROOT`| git-root / cwd       | The project root used for context hashing and the project-local store. |
| `AI_PLAYBOOK_SCROLLBACK_LINES` | `200`           | How many scrollback lines to capture for request context. |
| `AI_PLAYBOOK_MAX_FOLLOWUPS` | `3`                | Max auto-follow-ups on a failed verify (positive integer; non-positive/unparseable → the default). |

The harness itself — which one, its model, and its executable path — is configured
**only** via `[agent]` (`harness` / `model` / `bin` above). The former
`AI_PLAYBOOK_MODEL` / `ASSIST_MODEL` and `AI_PLAYBOOK_CLAUDE_BIN` env overrides
(and `AI_PLAYBOOK_CLAUDE_PERMISSION_MODE`) were retired with the legacy Claude
invocation path and are ignored.

### Internal / advanced

| Variable                            | Default            | Purpose |
|-------------------------------------|--------------------|---------|
| `AI_PLAYBOOK_DEBUG_LOG`             | unset (off)        | Path to a debug log file; when set, diagnostics are written there. |
| `AI_PLAYBOOK_USER_REQUEST`          | —                  | Internal: passes the request text between in-process stages. |
