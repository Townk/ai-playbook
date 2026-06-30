---
name: tidy-checkup
description: Run a quick health check on a project — build, test, and report status.
category: maintenance
tags:
  - build
  - test
  - health
created: 2026-06-30
---

# Tidy Checkup

> [!NOTE]
> This fixture is intended for browsing via the store (see chapter 08: `ai-playbook show tidy-checkup`). Its blocks assume a project cwd with `build.sh` and `test.sh` present.

A quick playbook to verify that a project builds and its tests pass.

## Build

```bash {id=build}
echo "Running build…"
bash build.sh
```

## Test

```bash {id=test needs=build}
echo "Running tests…"
bash test.sh
echo "All checks passed."
```
