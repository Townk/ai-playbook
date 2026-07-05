---
name: playbook-authoring
description: Use when authoring an ai-playbook runnable playbook (the {id=} fenced-block markdown schema) — schema reference, quality rubric, and validation loop
---

# Authoring ai-playbook playbooks

A playbook is literate-config Markdown: an H1 title, prose that walks a
teammate through the task, YAML front matter for metadata, and fenced code
blocks tagged on the language line to mark which are runnable. A GOOD playbook
is a sequence of checkpointed, individually confirmable steps that a person
(or an unattended `--auto` run) can run, verify, and undo.

## Schema quick-reference

### Front matter

A leading `---` … `---` YAML block:

| Key | Meaning |
|---|---|
| `name` | human title of the playbook |
| `description` | one-line summary |
| `category` | grouping label (e.g. `git`, `docker`, `setup`) |
| `tags` | search/filter keywords (list) |
| `env` | environment variables the playbook expects: a map of `NAME: {value, why}` |
| `created` | creation date; required non-empty, ISO `YYYY-MM-DD` by convention |
| `workdir` | target directory the playbook applies to (planned — not yet parsed) |
| `depends_on` | slugs of playbooks to run fully, in order, before this one |

`ai-playbook validate` requires `name`, `description`, `category`, and
`created` to be non-empty; write `created` as an ISO `YYYY-MM-DD` date by
convention. The rest are optional but recommended.
Declare `env:` entries like this:

```yaml
env:
  SERVICE_PORT:
    value: "8080"
    why: the port the service listens on
```

### Fence tags

Tags go on the opening fence's language line, e.g. ` ```bash {id=install} `.
The runner keys run/diff/apply and success detection on the `id`.

| Tag | Meaning |
|---|---|
| `{id=<id>}` | a runnable step |
| `{id=verify}` | the final whole-playbook verification; success detection keys on this block |
| `{needs=<id>[,<id>...]}` | gate: the block won't run until every listed id has succeeded |
| `{rollback=<undo-id>}` | declared on a FORWARD step, naming the companion block (by id) that undoes it; the named block renders as that step's rollback, not a numbered step |
| `{from=<id>}` | data edge: the named producer's stdout feeds this block's stdin (implies `needs=<id>`); `shell`/`run` blocks only |
| `file=<path>` | a create block: the block's entire payload becomes the file at `<path>`. A relative `<path>` resolves against the PROJECT ROOT — no `~` and no env-var expansion — and the body is written verbatim (a `${VAR}` in file content is never interpolated) |
| `{static}` | non-runnable illustration (no run button) |

Block types follow the language: `bash`/`sh`/`zsh` are shell steps;
`python`/`node`/`ruby`/`perl` are script steps; `diff`/`patch` blocks apply a
unified diff; non-executable languages (`text`, `console`, `output`, `log`,
`json`) are static even without the `{static}` flag; `file=` always makes a
create block.

Only TOP-LEVEL fenced blocks carry block authority: a tagged fence nested
inside a list or blockquote is inert — neither runnable, nor a rollback, nor
validated. Keep every runnable block at the top level of the document.

## The rubric — nine rules

1. **Atomicity — one logical step per block.** A block does ONE thing that a
   human confirms once: install one component, write one config, start one
   service. If describing a block needs an "and then…", split it. Multiple
   shell commands are fine ONLY when they form one atomic action
   (`mkdir && cd && tar`), never when they are separate steps.
2. **`file=` for file creation.** A new file's full content goes in a
   `{id=x file=<path>}` create block — NEVER a shell block with a heredoc /
   `cat >` / `tee`. The create block is previewable, undoable, and diffable;
   the heredoc is none of those.
3. **Diff blocks for edits.** Change an existing file with a diff block — a
   complete, `git apply`-able unified diff, paths relative to the project root
   — not `sed -i` one-liners when the change is structural, and never a
   rewrite-the-whole-file heredoc.
4. **Rollback discipline.** Every step that MUTATES state (installs, writes,
   enables, registers) declares `rollback=<undo-id>` in its fence tag, naming
   a companion `{id=<undo-id>}` block that restores the pre-step state. On
   failure, completed steps' rollbacks run in REVERSE order — each rollback
   only undoes its own step. Read-only steps (checks, queries) need none.
5. **Verify, always.** Every playbook ends with `{id=verify needs=<last-step>}`
   proving the GOAL state: a troubleshooting playbook re-runs the originally
   failing command; a how-to or onboarding playbook checks the installed /
   configured / running state. One block, one authoritative check.
6. **Real dependencies, declared.** `needs=` for ordering ("B requires A
   succeeded"), `from=` for data ("B consumes A's stdout on stdin" — prefer it
   when the consumer reads the whole output; the quoted `APB_OUT_<id>` env var
   and the raw-path `APB_OUT_FILE_<id>` cover argument-style access). Do not
   serialize independent steps.
7. **`{static}` for illustration.** Sample output, expected trees, error
   captures: static/console blocks — never runnable.
8. **Portability + `env:`.** Declare every required environment variable in
   the `env:` front matter (name + why); use `$PROJECT_ROOT`, `$HOME`, and
   tool-resolved paths instead of hardcoded ones. The playbook must be
   runnable on a machine that is not the author's.
9. **Callouts for danger.** A `warning` or `caution` callout — a blockquote
   opening with `> [!WARNING]` or `> [!CAUTION]` — precedes every destructive
   or irreversible step.

## Worked example — bad, then good

The task: bootstrap a small service on a fresh machine (write its config,
install it, enable it).

### BAD — one giant block, heredocs, no verify, no rollbacks

````markdown
```bash {id=setup}
mkdir -p ~/.config/demo-service
cat > ~/.config/demo-service/config.ini <<EOF
[server]
port = 8080
EOF
brew install demo-service
demo-service enable --port 8080
```
````

Everything is wrong at once: three separate steps fused into one block (a
human confirms them as a unit and a mid-block failure leaves unknown state),
the config file is written through a heredoc (not previewable, not undoable),
the port is hardcoded, nothing declares what mutates state or how to undo it,
and there is no verification that the service actually came up.

### GOOD — four atomic steps, `file=`, rollbacks, verify

````markdown
---
name: Bootstrap demo-service
description: Install, configure, and enable demo-service on a fresh machine
category: setup
tags: [bootstrap, service]
created: 2026-07-05
env:
  SERVICE_PORT:
    value: "8080"
    why: the port demo-service listens on
---

# Playbook — Bootstrap demo-service

## Goal

Install demo-service, point it at its config, and leave it running.

## How

Write the service configuration first. The file's full content lives in a
create block, so it is previewable, diffable, and undoable. Its relative path
lands under the project root, and the body is written verbatim (no `${VAR}`
placeholders in file content — env expansion happens only in shell steps):

```ini {id=config file=demo-service/config.ini}
[server]
log_level = info
```

Install the service. The step mutates the system, so it names its undo
companion:

```bash {id=install rollback=undo-install needs=config}
brew install demo-service
```

```bash {id=undo-install}
brew uninstall demo-service
```

Enable it, pointing at the config and passing the declared port — a shell
step DOES expand `${SERVICE_PORT}`:

```bash {id=enable rollback=undo-enable needs=install}
demo-service enable --config demo-service/config.ini --port "${SERVICE_PORT}"
```

```bash {id=undo-enable}
demo-service disable
```

Prove the goal state — the service answers on its port:

```bash {id=verify needs=enable}
curl -fsS "http://localhost:${SERVICE_PORT}/healthz"
```
````

Note the direction of the rollback pairing: the FORWARD step carries
`rollback=undo-install` in its fence tag, and the undo block is an ordinary
`{id=undo-install}` block. The undo block itself carries no `rollback=` tag.

## Authoring checklist

- [ ] One logical step per block — no fused "and then" blocks.
- [ ] Every new file is a `file=<path>` create block, never a heredoc.
- [ ] Every edit to an existing file is a complete unified-diff block.
- [ ] Every state-mutating step declares `rollback=<undo-id>` naming its undo block.
- [ ] The playbook ends with one `{id=verify}` block proving the goal state.
- [ ] `needs=`/`from=` declare the real dependency graph — nothing more.
- [ ] Illustrative output is `{static}`, never runnable.
- [ ] Every required variable is declared in `env:`; no hardcoded machine paths.
- [ ] A `warning`/`caution` callout precedes every destructive step.

## Validation loop

Iterate against the validator until it is clean:

```sh
ai-playbook validate --file <path>
```

Errors are contract violations — fix them, always. Warnings are advisory
quality findings (missing verify, missing rollbacks, heredoc file writes,
undeclared env vars): address each one, or consciously accept it when the
rubric genuinely does not apply. Re-run after every edit until the output is
quiet or every remaining warning is a deliberate choice.
