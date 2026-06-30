#!/usr/bin/env bash
set -euo pipefail

ITEMS=("apple" "banana" "cherry")

cmd="${1:-}"
case "$cmd" in
  list)
    echo "Items in shop:"
    for item in "${ITEMS[@]}"; do
      echo "  - $item"
    done
    ;;
  add)
    item="${2:-}"
    if [[ -z "$item" ]]; then
      echo "Usage: shop.sh add <item>" >&2
      exit 1
    fi
    echo "Added: $item"
    ;;
  *)
    echo "Usage: shop.sh <list|add <item>>"
    exit 1
    ;;
esac
