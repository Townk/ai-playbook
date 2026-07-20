# ai-playbook

[![CI](https://github.com/Townk/ai-playbook/actions/workflows/ci.yml/badge.svg)](https://github.com/Townk/ai-playbook/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/Townk/ai-playbook/branch/master/graph/badge.svg)](https://codecov.io/gh/Townk/ai-playbook)
[![Go Reference](https://pkg.go.dev/badge/github.com/Townk/ai-playbook.svg)](https://pkg.go.dev/github.com/Townk/ai-playbook)
[![Latest release](https://img.shields.io/github/v/release/Townk/ai-playbook)](https://github.com/Townk/ai-playbook/releases)
[![License](https://img.shields.io/github/license/Townk/ai-playbook)](LICENSE)

**ai-playbook** is a harness-agnostic, terminal-native AI assistant that turns
your live shell context into **runnable, reusable playbooks**. Instead of
copy-pasting one-off commands from a chat window, you get artifacts that can be
re-run, adapted to the current project, composed together, safely executed, and
validated.

Two entry verbs drive it: **`assist`** triages a request from your live terminal
context into a one-line command, a short answer, or a full playbook (reacting to
terminal failures and serving repeat requests from cache); **`create`** authors a
playbook directly. A **playbook store** then makes those playbooks
browsable/searchable (via an external picker fed by a machine-readable list),
re-runnable with adaptation to the current project, composable through
dependencies, safely executable (assisted / unattended + rollback), and lint-able
via `validate`.

## Install

### Homebrew (macOS & Linux)

```sh
brew install townk/tap/ai-playbook
```

One step installs all three binaries (`ai-playbook`, its short alias `apb`,
and `ask`) together with their man pages and zsh completions.

### Go toolchain / prebuilt binaries

Install the latest binary with the Go toolchain:

```sh
go install github.com/Townk/ai-playbook/cmd/ai-playbook@latest
```

Or download a prebuilt binary for your platform (linux / macOS ×
amd64 / arm64) from the [Releases](https://github.com/Townk/ai-playbook/releases)
page and place it on your `PATH`.

Check your version:

```sh
ai-playbook --version
```

The tool also installs as **`apb`**, a short alias for everyday typing —
same code, same commands/flags/help, just a shorter name:

```sh
go install github.com/Townk/ai-playbook/cmd/apb@latest
```

Every release archive ships the `apb` binary alongside `ai-playbook`, so you
can grab it from [Releases](https://github.com/Townk/ai-playbook/releases)
too. `apb --help` reads "apb" throughout.

A third binary, **`ask`**, exposes the same themed dialog widgets
(confirm/line/text/choose/form) as a standalone tool for shell scripts — see
[`ask` — themed dialogs for scripts](#ask--themed-dialogs-for-scripts) below.

```sh
go install github.com/Townk/ai-playbook/cmd/ask@latest
```

### Shell completion & man pages

The Homebrew formula installs both automatically. On any other route, the
binary can install its own — rendered at runtime from the same registry the
help text comes from, so they always match the installed version:

```sh
ai-playbook completion install   # _ai-playbook + _ask → ~/.local/share/zsh/site-functions
ai-playbook man install          # all man pages      → ~/.local/share/man/man1
```

Both take `--to <dir>` / `--force`, have matching `uninstall` subcommands
(idempotent — safe as package-manager hooks), and `completion show` prints the
script for piping. Make sure the completion directory is on your `fpath`
before `compinit`; the man default is found via the standard PATH-derived
manpath.

Alternatively, the release archives ship the same files. Each archive has a
zsh completion script, `_ai-playbook`
(subcommands, flags, and dynamic completion of your saved playbook slugs for
`run`/`show`/`edit`/`validate`/`env`) plus `_ask` for the `ask` binary. Copy
them into a directory on your `fpath` and let `compinit` pick them up:

```sh
mkdir -p ~/.zsh/completions
cp _ai-playbook _ask ~/.zsh/completions/
```

```sh
# ~/.zshrc
fpath=(~/.zsh/completions $fpath)
autoload -U compinit && compinit
```

The same archive ships generated man pages under `docs/man/ai-playbook*.1`
(one per command) plus a single `docs/man/ask.1` covering every `ask`
subcommand. Copy them into a `man1` directory on your `MANPATH`:

```sh
mkdir -p ~/.local/share/man/man1
cp docs/man/ai-playbook*.1 docs/man/ask.1 ~/.local/share/man/man1/
man ai-playbook
man ai-playbook-run
man ask
```

For quick reference without leaving the terminal, every command also has
inline help: `ai-playbook <command> --help`.

## Model backends

ai-playbook drives an agent CLI you already have installed and authenticated —
it never talks to a model API directly. Three harnesses ship, selected with
`[agent] harness` in the [config file](docs/configuration.md):

- **`claude`** (the default) — Claude Code, FULL tier: structured playbook
  drafting, agent `run`/`ask` tools, and knowledge capture.
- **`pi`** — the pi coding agent, FULL tier via an embedded pi extension that
  carries the same tools.
- **`cursor`** — Cursor's CLI agent, FULL tier: the same structured drafting,
  tools, and knowledge capture, carried by a config-root redirect that isolates
  ai-playbook's MCP tools, with an allowlist hook + scratch working directory
  containing cursor's builtin tools (both live-verified).

Per-harness defaults (model, triage model, thinking, binary) are documented in
the [configuration reference](docs/configuration.md).

## Usage

The target command surface (see [`docs/ROADMAP.md`](docs/ROADMAP.md) for status
per phase):

```
assist [<prompt>]                      triage → command/answer/playbook;
                                       cache badge; interactive entry

create <prompt> [--template <t>]       author a playbook directly
                                       (always fresh; writes store+cache)

list   [--format human                 list the playbook store in
       | fuzzy-data-source             different formats
       | json]

search <query> [--format ...]          filter the store

show   <slug>                          render a playbook (read-only)

run    [[--playbook] <slug>            execute a playbook
       | --file <path>]
       [--retry]
       [--assisted
       | --auto [--no-auto-rollback]
                [--with-env <json|file>]
                [--junit <path>]]

edit   <slug>                          open the playbook in $EDITOR

validate [<slug> | --file <path>]      AI + structural review of a playbook

env    [<slug> | --file <path>]        print declared env as --with-env JSON
                                       (resolved from the environment; secrets
                                       redacted)

kb     <show|edit|search|list>         browse/edit/search the knowledge base
                                       (see "Knowledge base" below)

completion <show|install|uninstall>    print or (un)install the zsh completions
man        <install|uninstall>         (un)install the man pages

skill  <show|install>                  print or install the playbook-authoring
       [--to <dir>] [--force]          skill (see "Authoring quality" below)
```

Run modes (mutually exclusive): the default is an interactive pager (free-form),
`--assisted` is a guided confirm-each-step run, and `--auto` is unattended;
`--no-auto-rollback` is valid only with `--auto`.

For project-bound playbooks, `--auto --with-env '{…}'` (or a path to a JSON file)
supplies declared `env:` values on the CLI, and `env <slug>` scaffolds that JSON
from a playbook's declaration — resolving current values and leaving secrets
empty. `--auto --junit <path>` additionally writes the run's results as a
JUnit-XML report for CI test-reporter ingestion. A playbook may also declare
`depends_on: [slug, …]`: its transitive dependencies run headless, in
topological order, before it.

### The run journal & resuming a failed run (`--retry`)

Every playbook run — viewer or `--auto` — journals its latest state to
`<data-root>/projects/<key>/runs/` (default data root
`~/.local/share/ai-playbook`): per-block outcomes, exit codes, and durations,
updated crash-safely after every block. One journal per playbook per project,
latest run only; journals are advisory metadata — a missing or corrupt journal
never breaks anything.

When a run fails mid-way on something you fix out-of-band, `run --retry` picks
it back up: it refuses when there is nothing to resume (no journal, or the
last run succeeded) or when the playbook changed since the failed run;
otherwise the blocks that already succeeded start pre-seeded as
"done — previous run" (the `verify` block never — the goal is always
re-proven), execution resumes at the first failed/unrun block, and any
already-done producer whose output a remaining block still consumes (`from=`
or `$APB_*` references) re-runs first, since captures don't persist across
sessions. `--retry` composes with `--auto`, `--assisted`, and `--file`.

Two discoverability surfaces ride the same journal: a plain `run` over a
failed, stopped, or interrupted (crashed/killed mid-run) last run prints a
one-line stderr hint (`last run failed at "two" (2h ago) — 'ai-playbook run
--retry …' resumes there`; the verb follows the outcome — `stopped`, `was
interrupted`), and `list`'s **LAST RUN** column — `search` shares the same
table — shows each playbook's last outcome in the current project: `✓`/`✗`
plus the run's total elapsed, a bare `✗` for a run interrupted mid-flight,
or `–` when never run. Note the journal tracks the playbook you ran:
`depends_on` parents' dependency runs are not journaled, so a retried parent
re-runs its dependency chain fresh.

### Piping a block's output into the next step

A `from=<id>` fence attribute wires a producer block's retained stdout
straight into a consumer's **stdin** — no shell-quoting, no size limit, and it
works for script (`run`) blocks too, so a Python filter can read `sys.stdin`
directly:

````markdown
```bash {id=build}
echo '{"count": 3}'
```

```python {id=filter from=build}
import json, sys
print(json.load(sys.stdin)["count"] * 2)
```

```bash {id=consume needs=filter}
echo "doubled: $(cat "$APB_OUT_FILE_filter")"
```
````

Running `consume` auto-materializes the whole chain: `build` and `filter` run
first (each its own status/log), then `consume`. `from=` implies `needs=`, so
ordering and gating follow the data edge; `$APB_OUT_FILE_<id>` (the raw path
to a block's retained stdout) is the idiom for pulling a prior step's output
into another step's *arguments* rather than its stdin. See
[the playbook schema](docs/specifications/playbook-schema.md#value-passing)
for the full contract.

## Authoring quality (the rubric)

What separates a runnable-but-poor playbook from a good one — atomic steps,
`file=` create blocks instead of heredocs, a rollback per mutating step, a
final `verify` block, declared `env:` — is codified as a nine-rule rubric in
[`docs/specifications/playbook-authoring.md`](docs/specifications/playbook-authoring.md).
The tool teaches it everywhere authoring happens: the AI authoring prompts
embed it, and `ai-playbook validate` reports rubric violations as advisory
warnings (missing verify, missing rollbacks, heredoc file writes, undeclared
env vars) plus an AI review pass that judges against the same rubric.

For authoring playbooks with an external agent, the same guidance ships as a
portable skill:

```sh
ai-playbook skill install        # → ~/.claude/skills/… (Claude Code)
ai-playbook skill install --to <dir>   # any other harness's skills root
ai-playbook skill show           # print the SKILL markdown; pipe it anywhere
```

## Knowledge base (remember / recall)

Every authoring call can distill durable lessons via the `remember` tool, filed
into two knowledge sets: a **global** file (`## System` tooling/machine truths,
`## User` who you are/prefer) shared across every project, and a per-project
file (`## Environment` this project's setup, `## Topics` domain-specific
lessons). Both files fold back into every authoring-shaped call — assist,
create, follow-ups, and drift regeneration all see what was learned before,
without a second round trip. Oversized files get compacted (merged/
generalized/trimmed, with a `.bak` backup) automatically at solution
completion.

The `kb` verb browses and edits that state directly:

```sh
ai-playbook kb show                        # global + this project's facts
ai-playbook kb edit --global               # open the global file in $EDITOR
ai-playbook kb search --all "docker"       # substring search across all projects
ai-playbook kb list                        # every knowledge file, size + fact count
```

See [`docs/specifications/knowledge-base.md`](docs/specifications/knowledge-base.md)
for the full storage/routing contract.

## `ask` — themed dialogs for scripts

`ask` is a standalone binary exposing ai-playbook's themed dialog widgets
(confirm/line/text/choose/form) as a pure-I/O tool for shell scripts: a
submitted value (if any) on stdout, diagnostics on stderr, and the outcome
encoded in the exit code — no ai-playbook store, playbook, or session
required. One example per widget:

```sh
ask confirm "Delete the branch?" --danger      # exit code IS the answer
name=$(ask line "Your name" --value "$USER")
notes=$(ask text "Release notes")
env=$(ask choose "Target environment" staging production)
ask form --spec ./fields.json --json           # JSON spec in, JSON object out
```

**Exit codes:** `0` submit/affirmative, `130` cancel (Esc/Ctrl-C), `2` usage
error (bad flags, malformed JSON form spec, or no terminal reachable). `1` is
confirm's negative answer, but also **any other non-usage runtime widget
failure** — the two are indistinguishable by exit code alone. That's the
safe direction for `if ask confirm "..."; then …`: both a genuine "No" and an
unexpected failure take the same false branch; a script that must tell them
apart should check stderr.

**Theming:** every `--theme-*` flag has an `ASK_<FLAG>` environment fallback
(e.g. `--theme-accent` / `ASK_THEME_ACCENT`), so a script can export the
palette once instead of passing it on every call. Precedence is flag > env >
built-in default. A **set-but-empty** `ASK_<FLAG>` variable deliberately
overrides the default with the empty string — presence wins, not
non-emptiness — so `export ASK_THEME_ACCENT=` intentionally blanks that
color, while leaving the variable unset entirely keeps the default.

See `ask --help`, `ask <command> --help`, or `man ask` for the full flag
reference.

## Documentation

- [Roadmap](docs/ROADMAP.md) — vision, command surface, phases, and shipped foundations.
- [Getting started](docs/guides/getting-started.md)
- [Authoring playbooks](docs/guides/authoring-playbooks.md)
- [Running playbooks](docs/guides/running-playbooks.md)
- [The store](docs/guides/the-store.md)
- [Configuration](docs/configuration.md)
- [Architecture overview](docs/architecture/overview.md) and [ADRs](docs/architecture/adrs)
- [Backlog](docs/BACKLOG.md) and [Changelog](CHANGELOG.md)
