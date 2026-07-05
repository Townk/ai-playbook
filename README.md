# ai-playbook

[![CI](https://github.com/Townk/ai-playbook/actions/workflows/ci.yml/badge.svg)](https://github.com/Townk/ai-playbook/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/Townk/ai-playbook/branch/master/graph/badge.svg)](https://codecov.io/gh/Townk/ai-playbook)
[![Go Report Card](https://goreportcard.com/badge/github.com/Townk/ai-playbook)](https://goreportcard.com/report/github.com/Townk/ai-playbook)
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

Every release archive also ships a zsh completion script, `_ai-playbook`
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
       [--assisted
       | --auto [--no-auto-rollback]
                [--with-env <json|file>]]

edit   <slug>                          open the playbook in $EDITOR

validate [<slug> | --file <path>]      AI + structural review of a playbook

env    [<slug> | --file <path>]        print declared env as --with-env JSON
                                       (resolved from the environment; secrets
                                       redacted)

kb     <show|edit|search|list>         browse/edit/search the knowledge base
                                       (see "Knowledge base" below)
```

Run modes (mutually exclusive): the default is an interactive pager (free-form),
`--assisted` is a guided confirm-each-step run, and `--auto` is unattended;
`--no-auto-rollback` is valid only with `--auto`.

For project-bound playbooks, `--auto --with-env '{…}'` (or a path to a JSON file)
supplies declared `env:` values on the CLI, and `env <slug>` scaffolds that JSON
from a playbook's declaration — resolving current values and leaving secrets
empty. A playbook may also declare `depends_on: [slug, …]`: its transitive
dependencies run headless, in topological order, before it.

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
