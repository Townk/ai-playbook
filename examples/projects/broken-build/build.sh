#!/usr/bin/env bash
set -euo pipefail
version=$(cat version.txt)   # FAILS: version.txt missing (the file is VERSION)
echo "broken-build: building version $version"
echo "broken-build: build OK"
