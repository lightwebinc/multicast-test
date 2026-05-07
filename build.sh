#!/usr/bin/env bash
# Build all three lab binaries from local source and stage them in /tmp.
#
# ansible/run-deploy.sh already builds as part of a full deploy cycle.
# Use this script to verify clean compilation independently, or to pre-stage
# binaries before running:  SKIP_LOCAL_BUILD=1 bash ansible/run-deploy.sh
#
# Usage: bash build.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"

BUILD_ENV=(
  GOOS=linux
  GOARCH=amd64
  CGO_ENABLED=0
)

build_one() {
  local label="$1"
  local src_dir="$2"
  local out="$3"

  echo "==> Building $label ..."
  if [ ! -d "$src_dir" ]; then
    echo "ERROR: source directory not found: $src_dir" >&2
    exit 1
  fi
  (cd "$src_dir" && env "${BUILD_ENV[@]}" go build -buildvcs=false -o "$out" .)
  printf "    ok  %s  (%s)\n" "$out" "$(du -sh "$out" | cut -f1)"
}

echo "================================================="
echo " bitcoin-multicast-test — build all binaries"
echo "================================================="
echo ""

build_one "bitcoin-shard-proxy"    "$REPO_ROOT/bitcoin-shard-proxy"    /tmp/bitcoin-shard-proxy
build_one "bitcoin-shard-listener" "$REPO_ROOT/bitcoin-shard-listener" /tmp/bitcoin-shard-listener
build_one "bitcoin-retry-endpoint" "$REPO_ROOT/bitcoin-retry-endpoint" /tmp/bitcoin-retry-endpoint

echo ""
echo "All binaries staged in /tmp — ready for: bash ansible/run-deploy.sh"
