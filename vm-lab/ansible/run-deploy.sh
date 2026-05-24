#!/usr/bin/env bash
# Deploy bitcoin-ingress (proxy), bitcoin-retransmission (retry1..N) and
# bitcoin-listener (listener1..3) using inventories committed to this repo.
# Idempotent.
#
# All three Go services have `replace ../bitcoin-shard-common` in their go.mod,
# which breaks remote builds (the parent repo is missing on the VMs). To work
# around that, this script builds all three binaries locally and feeds them
# to the roles via the {proxy,listener,retry}_local_binary opt-ins. Set
# SKIP_LOCAL_BUILD=1 to fall back to the role's git-clone-and-build path
# (only useful once shard-common is published as a tagged module).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INGRESS_DIR=${BITCOIN_INGRESS_DIR:-$HOME/repo/bitcoin-ingress/ansible}
LISTENER_DIR=${BITCOIN_LISTENER_DIR:-$HOME/repo/bitcoin-listener/ansible}
RETRANS_DIR=${BITCOIN_RETRANSMISSION_DIR:-$HOME/repo/bitcoin-retransmission/ansible}

PROXY_SRC=${BITCOIN_PROXY_SRC:-$HOME/repo/bitcoin-shard-proxy}
LISTENER_SRC=${BITCOIN_LISTENER_SRC:-$HOME/repo/bitcoin-shard-listener}
RETRY_SRC=${BITCOIN_RETRY_SRC:-$HOME/repo/bitcoin-retry-endpoint}

for d in "$INGRESS_DIR" "$LISTENER_DIR" "$RETRANS_DIR"; do
  if [[ ! -f "$d/site.yml" ]]; then
    echo "ERROR: expected playbook at $d/site.yml" >&2
    echo "       override with BITCOIN_INGRESS_DIR / BITCOIN_LISTENER_DIR / BITCOIN_RETRANSMISSION_DIR" >&2
    exit 1
  fi
done

EXTRA_PROXY=()
EXTRA_LISTENER=()
EXTRA_RETRY=()

if [[ -z "${SKIP_LOCAL_BUILD:-}" ]]; then
  echo "==> Building Go binaries locally (linux/amd64) for all three services"
  build_one() {
    local src="$1" out="$2" name="$3"
    if [[ ! -f "$src/go.mod" ]]; then
      echo "ERROR: $name source dir $src missing go.mod" >&2
      exit 1
    fi
    (cd "$src" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
      go build -buildvcs=false -o "$out" .)
    echo "    built $name -> $out ($(stat -c%s "$out") bytes)"
  }
  build_one "$PROXY_SRC"    /tmp/bitcoin-shard-proxy    bitcoin-shard-proxy
  build_one "$LISTENER_SRC" /tmp/bitcoin-shard-listener bitcoin-shard-listener
  build_one "$RETRY_SRC"    /tmp/bitcoin-retry-endpoint bitcoin-retry-endpoint

  EXTRA_PROXY=(-e proxy_local_binary=/tmp/bitcoin-shard-proxy)
  EXTRA_LISTENER=(-e listener_local_binary=/tmp/bitcoin-shard-listener)
  EXTRA_RETRY=(-e retry_local_binary=/tmp/bitcoin-retry-endpoint)
fi

echo "==> Deploying bitcoin-shard-proxy to proxy VM"
(cd "$INGRESS_DIR" && ansible-playbook -i "$SCRIPT_DIR/ingress-hosts.yml" site.yml "${EXTRA_PROXY[@]}" "$@")

echo "==> Deploying bitcoin-retry-endpoint to retry1..N"
(cd "$RETRANS_DIR" && ansible-playbook -i "$SCRIPT_DIR/retry-hosts.yml" site.yml "${EXTRA_RETRY[@]}" "$@")

echo "==> Deploying bitcoin-shard-listener to listener1..3"
(cd "$LISTENER_DIR" && ansible-playbook -i "$SCRIPT_DIR/listener-hosts.yml" site.yml "${EXTRA_LISTENER[@]}" "$@")

echo "==> Deploy complete."
