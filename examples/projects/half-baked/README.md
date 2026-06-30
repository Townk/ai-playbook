## half-baked

A partially-complete fake project — it ships with a missing file and a stale config value.

- `config.yml` ships a stale `version: "1.0"` — ch.04 patches it to `"2.0"` via a diff block.
- `config.local.yml` is intentionally absent — ch.03 creates it via a `file=` block.
