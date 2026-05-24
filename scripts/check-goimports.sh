#!/usr/bin/env bash
set -euo pipefail

if ! command -v goimports >/dev/null 2>&1; then
  echo "goimports is required. Install with: go install golang.org/x/tools/cmd/goimports@latest" >&2
  exit 1
fi

files="$(goimports -l .)"
if [[ -n "$files" ]]; then
  echo "goimports needed for:" >&2
  echo "$files" >&2
  exit 1
fi
