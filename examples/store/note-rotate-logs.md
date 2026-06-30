---
name: note-rotate-logs
description: Rotate and compress application log files, keeping the last 7 days.
category: operations
tags:
  - logs
  - rotation
  - cleanup
created: 2026-06-30
---

# Rotate Logs

Compresses yesterday's log file and removes any logs older than 7 days.

## Compress yesterday's log

```bash {id=compress}
log_file="app-$(date -v-1d +%Y-%m-%d).log"
if [[ -f "$log_file" ]]; then
  gzip "$log_file"
  echo "Compressed: ${log_file}.gz"
else
  echo "No log file found for yesterday: $log_file"
fi
```

## Remove old logs

```bash {id=cleanup needs=compress}
find . -name "app-*.log.gz" -mtime +7 -print -delete
echo "Old logs removed."
```
