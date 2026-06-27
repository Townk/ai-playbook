# On-disk storage layout

ai-playbook persists two kinds of data under a single **data dir**: the playbook
**store** (the durable, browsable library) and the response **cache** (context-hash
keyed, for `assist` serves). There is also a **project-local** store for
docs-as-code playbooks committed inside a repository. Derived from
[`../ROADMAP.md`](../ROADMAP.md), the Phase 1 spec
([`phase-1-live-playbook-store.md`](phase-1-live-playbook-store.md)), and
`cache/cache.go`.

## The data dir

Resolution order (highest priority first):

1. `AI_PLAYBOOK_DATA_DIR` — explicit override.
2. `${XDG_DATA_HOME:-~/.local/share}/ai-playbook` — the default.

Layout:

```
<data-dir>/
  playbooks/                       the global store: <slug>.md (slug = filename stem)
  cache/
    <ctxhash>/
      <reqhash>.md                 a cache entry (YAML front matter + body)
      <reqhash>.request.json       sidecar: the original request.json (faithful regenerate)
```

### The cache key

- **`<ctxhash>`** — `ContextHash`: a sha256 over `v1` + the project root. When the
  last command **failed** (non-zero exit), it also folds in the command text, the
  exit code, and the normalized scrollback (that is the error-diagnosis context).
  A successful or absent last command keys on the **project only**, so the bucket
  stays stable regardless of what was last run.
- **`<reqhash>`** — `RequestHash`: a sha256 over the whitespace-trimmed request
  text.

Each entry begins with `schema: ai-playbook-cache/v1` front matter
(`kind`, `context_hash`, `request_hash`, `created_at`, plus extras), followed by
the body. Writes are atomic (temp file + rename).

## The project-local store

`${PROJECT_ROOT}/.ai-playbook/playbooks/*.md` — playbooks committed inside a
repository so they travel with the project (docs-as-code). `PROJECT_ROOT` comes
from the captured context / `AI_PLAYBOOK_PROJECT_ROOT`, with a git-root fallback.

Project-local playbooks get a **`proj:`-prefixed slug** (`proj:<stem>`) so they are
referenced unambiguously and never shadow global playbooks: a bare slug resolves to
the global store; `proj:<stem>` resolves to the project store. Both stores are
scanned together (on-demand, no DB); a malformed file is skipped (logged), never
fatal.

## The `fuzzy-data-source` line format

`list` / `search --format fuzzy-data-source` emit one record per line, delimited by
`\x1f` (US, the ASCII unit separator):

```
<display>\x1f<slug>\x1f<path>
```

- `<display>` (field 1) — `<name> — <description> · <category> · <tags>`; the
  picker shows and searches this (e.g. `fzf --with-nth 1`).
- `<slug>` (field 2) — the run target (`ai-playbook run {2}`).
- `<path>` (field 3) — the absolute file path (so a picker need not re-resolve;
  `ai-playbook edit {2}` for editing).
