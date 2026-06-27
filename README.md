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
       | --auto [--no-auto-rollback]]

edit   <slug>                          open the playbook in $EDITOR

validate [<slug> | --file <path>]      AI + structural review of a playbook
```

Run modes (mutually exclusive): the default is an interactive pager (free-form),
`--assisted` is a guided confirm-each-step run, and `--auto` is unattended;
`--no-auto-rollback` is valid only with `--auto`.

## Documentation

- [Roadmap](docs/ROADMAP.md) — vision, command surface, phases, and shipped foundations.
- [Getting started](docs/guides/getting-started.md)
- [Authoring playbooks](docs/guides/authoring-playbooks.md)
- [Running playbooks](docs/guides/running-playbooks.md)
- [The store](docs/guides/the-store.md)
- [Configuration](docs/configuration.md)
- [Architecture overview](docs/architecture/overview.md) and [ADRs](docs/architecture/adrs)
- [Backlog](docs/BACKLOG.md) and [Changelog](CHANGELOG.md)
