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
(flags, stream parser) is owned in-tree — you only pick which harness plus these.

| Key            | Default    | Meaning |
|----------------|------------|---------|
| `harness`      | `claude`   | Which shipped harness to drive (Claude today; pi/cursor are additive later). |
| `model`        | `""`       | Model id passed to the harness for authoring; empty → the harness default. |
| `triage_model` | `haiku`    | Model id for the cheap one-shot CLASSIFY pass (command/answer/escalate) — a fast/cheap alias so a quick classify never burns the capable model. |
| `bin`          | `""`       | Override for the harness executable path; empty → the harness name resolved on `PATH`. |
| `thinking`     | `medium`   | Reasoning effort for the owned Claude invocation, mapped to a `MAX_THINKING_TOKENS` budget: `off` / `low` / `medium` / `high`, or a bare integer. `medium` ≈ 8000 tokens; `off` disables thinking. |

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

Example:

```toml
[agent]
harness = "claude"
model = "sonnet"
triage_model = "haiku"
thinking = "medium"

[driver]
shell = "bash"   # pin bash; omit (or "") to auto-honor $SHELL

[mux]
backend = "zellij"   # opt in to the multiplexer (off by default)
# tier-2: override a single action only if you are not on the default zellij setup
dump-screen = "tmux capture-pane -p {panearg}"
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
| `AI_PLAYBOOK_RUN_TIMEOUT` | `120` (seconds)      | Timeout for a single run-block execution. |
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
