#!/usr/bin/env bash
# Fails if core packages violate import boundary rules.
# Run from the repository root: scripts/check-boundaries.sh
set -euo pipefail

CORE_PKG="github.com/smasonuk/falken-core/pkg/falken"
CORE_PATTERNS=("./pkg/falken" "./internal/...")

# These must not appear in direct imports of pkg/falken.
FORBIDDEN_DIRECT=(
  "github.com/docker/docker"
  "github.com/smasonuk/falken-extra"
  "github.com/smasonuk/falken-core/internal/sandbox"
  "github.com/smasonuk/falken-core/internal/netproxy"
)

# These must not appear anywhere in the transitive dependency closure of core
# library packages.
FORBIDDEN_TRANSITIVE=(
  "github.com/docker/docker"
  "github.com/opencontainers/image-spec"
  "github.com/smasonuk/falken-extra"
  "github.com/smasonuk/falken-core/internal/netproxy"
  "github.com/smasonuk/falken-core/internal/sandbox"
  "github.com/smasonuk/falken-core/internal/wasmhost"
  "github.com/smasonuk/falken-core/internal/extensions/wasmtool"
  "github.com/tmc/langchaingo"
  "github.com/tmc/langchaingo/llms/openai"
  "github.com/tetratelabs/wazero"
)

failed=0

direct_imports=$(go list -f '{{join .Imports "\n"}}' "$CORE_PKG" 2>&1)
for pkg in "${FORBIDDEN_DIRECT[@]}"; do
  if echo "$direct_imports" | grep -qF "$pkg"; then
    echo "FAIL: $CORE_PKG directly imports forbidden package: $pkg" >&2
    failed=1
  fi
done

transitive_imports=$(go list -deps "${CORE_PATTERNS[@]}" 2>&1)
for pkg in "${FORBIDDEN_TRANSITIVE[@]}"; do
  if echo "$transitive_imports" | grep -qF "$pkg"; then
    echo "FAIL: core library packages transitively import forbidden package: $pkg" >&2
    failed=1
  fi
done

if [[ -d internal/sandbox ]]; then
  echo "FAIL: core contains internal/sandbox; Docker sandbox runtimes belong outside falken-core" >&2
  failed=1
fi

if [[ -d internal/wasmhost || -d internal/extensions/wasmtool || -d internal/extensions/plugins ]]; then
  echo "FAIL: core contains old Wasm extension runtime directories; Wasm belongs in falken-extra/wasmtools" >&2
  failed=1
fi

if grep -R --include='*.go' -nE 'NewContainerRuntime|DockerEngineRuntime|exec\.Command(Context)?\(.*"docker"' internal pkg >/tmp/falken-core-boundary-source.txt 2>/dev/null; then
  echo "FAIL: core source appears to contain Docker runtime implementation code:" >&2
  cat /tmp/falken-core-boundary-source.txt >&2
  failed=1
fi

if [[ $failed -eq 0 ]]; then
  echo "OK: core library packages pass all import boundary checks"
fi

exit $failed
