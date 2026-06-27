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

### `[mux]`

Command **templates** for terminal-multiplexer actions (defaults target zellij).
Each value is a template string; the binary token-splits it, substitutes
placeholders (`{cmd}`, `{cwd}`/`{cwdarg}`, `{pane}`/`{panearg}`, `{width}`,
`{height}`, `{name}`/`{namearg}`, `{text}`, `{title}`), and runs the resulting argv
directly (it is **not** a shell). An empty value after merge means the action is
unconfigured.

| Key                  | Action |
|----------------------|--------|
| `open-floating-pane` | Spawn a floating pane (the thinking/working float). |
| `open-input-float`   | Spawn the borderless, pinned request/ask input float. |
| `open-docked-pane`   | Spawn a docked pane (to the right). |
| `dump-screen`        | Capture a pane's scrollback (for context capture). |
| `type-into-pane`     | Write characters into the origin pane (targets `--pane-id`). |

Defaults are the zellij commands from `config.Default()`; see `config/config.go` for
the exact template strings and the placeholder/argv-safety contract.

Example:

```toml
[agent]
harness = "claude"
model = "sonnet"
triage_model = "haiku"
thinking = "medium"

[mux]
# override only if you are not on the default zellij setup
dump-screen = "tmux capture-pane -p {panearg}"
```

## Environment variables

All ai-playbook env vars are prefixed `AI_PLAYBOOK_`.

### User-facing

| Variable                  | Default              | Purpose |
|---------------------------|----------------------|---------|
| `AI_PLAYBOOK_DATA_DIR`    | `${XDG_DATA_HOME:-~/.local/share}/ai-playbook` | The data dir: the playbook store + the response cache (see [storage](specifications/storage.md)). Highest-priority override. |
| `AI_PLAYBOOK_MODEL`       | harness default      | Override the authoring model id (same role as `[agent] model`). |
| `AI_PLAYBOOK_CLAUDE_BIN`  | `claude` on `PATH`   | Path to the Claude CLI executable. |
| `AI_PLAYBOOK_NO_CACHE`    | unset (cache on)     | When set (non-empty), disable cache serving (always escalate/author). |
| `AI_PLAYBOOK_PROJECT_ROOT`| git-root / cwd       | The project root used for context hashing and the project-local store. |
| `AI_PLAYBOOK_SCROLLBACK_LINES` | `200`           | How many scrollback lines to capture for request context. |
| `AI_PLAYBOOK_RUN_TIMEOUT` | `120` (seconds)      | Timeout for a single run-block execution. |
| `AI_PLAYBOOK_MAX_FOLLOWUPS` | `3`                | Max auto-follow-ups on a failed verify (positive integer; non-positive/unparseable → the default). |

### Internal / advanced

| Variable                            | Default            | Purpose |
|-------------------------------------|--------------------|---------|
| `AI_PLAYBOOK_DEBUG_LOG`             | unset (off)        | Path to a debug log file; when set, diagnostics are written there. |
| `AI_PLAYBOOK_USER_REQUEST`          | —                  | Internal: passes the request text between in-process stages. |
| `AI_PLAYBOOK_CLAUDE_PERMISSION_MODE`| `bypassPermissions`| The headless permission posture for the Claude invocation, so the agent never blocks on an interactive prompt. |
| `AI_PLAYBOOK_HUNK_BIN`              | `hunk` on `PATH`   | Override for the `hunk` diff viewer (falls back hunk → delta → less); used in tests. |
