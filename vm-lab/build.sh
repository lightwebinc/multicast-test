#!/usr/bin/env bash
# Build all four lab binaries from local source and stage them in /tmp.
#
# ansible/run-deploy.sh already builds the three service binaries as part of
# a full deploy cycle.  subtx-gen is NOT built by Ansible — this script also
# builds and pushes it to the `source` LXD VM.
#
# Use this script to verify clean compilation independently, or to pre-stage
# binaries before running:  SKIP_LOCAL_BUILD=1 bash ansible/run-deploy.sh
#
# Usage: bash build.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# vm-lab/ lives inside the bitcoin-multicast-test repo; sibling repos
# (bitcoin-shard-proxy, bitcoin-shard-listener, ...) are checked out alongside
# it under the parent directory.
REPO_ROOT="$(dirname "$(dirname "$SCRIPT_DIR")")"

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

push_source_vm() {
  local bin="$1"
  local dest="$2"
  echo "==> Pushing $(basename "$bin") to source VM ..."
  lxc file push "$bin" "$dest"
  lxc exec source -- chmod +x "/usr/local/bin/$(basename "$bin")"
  printf "    ok  source:/usr/local/bin/%s\n" "$(basename "$bin")"
}

echo "================================================="
echo " bitcoin-multicast-test — build all binaries"
echo "================================================="
echo ""

build_one "bitcoin-shard-proxy"    "$REPO_ROOT/bitcoin-shard-proxy"    /tmp/bitcoin-shard-proxy
build_one "bitcoin-shard-listener" "$REPO_ROOT/bitcoin-shard-listener" /tmp/bitcoin-shard-listener
build_one "bitcoin-retry-endpoint" "$REPO_ROOT/bitcoin-retry-endpoint" /tmp/bitcoin-retry-endpoint
build_one "subtx-gen"            "$REPO_ROOT/bitcoin-subtx-generator/cmd/subtx-gen"            /tmp/subtx-gen
build_one "send-block-announce"  "$REPO_ROOT/bitcoin-subtx-generator/cmd/send-block-announce"  /tmp/send-block-announce
build_one "send-subtree-data"    "$REPO_ROOT/bitcoin-subtx-generator/cmd/send-subtree-data"    /tmp/send-subtree-data

echo ""
echo "==> Pushing source VM binaries"
push_source_vm /tmp/subtx-gen           source/usr/local/bin/subtx-gen
push_source_vm /tmp/send-block-announce source/usr/local/bin/send-block-announce
push_source_vm /tmp/send-subtree-data   source/usr/local/bin/send-subtree-data

echo ""
echo "Service binaries staged in /tmp — ready for: bash ansible/run-deploy.sh"
