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

### Shell completion & man pages

Every release archive also ships a zsh completion script, `_ai-playbook`
(subcommands, flags, and dynamic completion of your saved playbook slugs for
`run`/`show`/`edit`/`validate`/`env`). Copy it into a directory on your
`fpath` and let `compinit` pick it up:

```sh
mkdir -p ~/.zsh/completions
cp _ai-playbook ~/.zsh/completions/
```

```sh
# ~/.zshrc
fpath=(~/.zsh/completions $fpath)
autoload -U compinit && compinit
```

The same archive ships generated man pages under `docs/man/ai-playbook*.1`
(one per command). Copy them into a `man1` directory on your `MANPATH`:

```sh
mkdir -p ~/.local/share/man/man1
cp docs/man/ai-playbook*.1 ~/.local/share/man/man1/
man ai-playbook
man ai-playbook-run
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
```

Run modes (mutually exclusive): the default is an interactive pager (free-form),
`--assisted` is a guided confirm-each-step run, and `--auto` is unattended;
`--no-auto-rollback` is valid only with `--auto`.

For project-bound playbooks, `--auto --with-env '{…}'` (or a path to a JSON file)
supplies declared `env:` values on the CLI, and `env <slug>` scaffolds that JSON
from a playbook's declaration — resolving current values and leaving secrets
empty. A playbook may also declare `depends_on: [slug, …]`: its transitive
dependencies run headless, in topological order, before it.

## Documentation

- [Roadmap](docs/ROADMAP.md) — vision, command surface, phases, and shipped foundations.
- [Getting started](docs/guides/getting-started.md)
- [Authoring playbooks](docs/guides/authoring-playbooks.md)
- [Running playbooks](docs/guides/running-playbooks.md)
- [The store](docs/guides/the-store.md)
- [Configuration](docs/configuration.md)
- [Architecture overview](docs/architecture/overview.md) and [ADRs](docs/architecture/adrs)
- [Backlog](docs/BACKLOG.md) and [Changelog](CHANGELOG.md)
