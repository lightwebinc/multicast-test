#!/bin/bash
# up.sh: bring up the ITCVE-2 joined processor, Teranode on an isolated regtest
# chain, from teranode-quickstart at a pinned commit with every image by digest.
#
#   tpnil/joined/processor/up.sh [WORK]      (default WORK=/scratch/tpnil-joined)
#
# The answers setup.sh would ask for are fixed here: regtest, full mode (so the
# p2p service announces blocks and subtrees to a local Merkle Service), no
# archival node, no monitoring stack. RPC credentials and the gRPC admin key
# are generated into the quickstart's .env, which is never printed.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=${1:-/scratch/tpnil-joined}
QS_REPO=https://github.com/bsv-blockchain/teranode-quickstart.git
QS_COMMIT=90ff6bf12010e7681dba6bbecb07c2eb96323e07
TERANODE_VERSION=v0.16.0@sha256:8ff56f976ef406309c49ff7e6c41e193d9dca7537bfe93976ec07b52c6d3a220
QS=$WORK/teranode-quickstart

say() { echo "== $(date -u +%H:%M:%S) $*"; }
die() { echo "processor/up.sh: $*" >&2; exit 1; }

mkdir -p "$WORK"
if [ ! -d "$QS/.git" ]; then
	git clone -q "$QS_REPO" "$QS"
fi
git -C "$QS" fetch -q origin
git -C "$QS" -c advice.detachedHead=false checkout -q "$QS_COMMIT"
[ -z "$(git -C "$QS" status --porcelain --untracked-files=no)" ] || die "tracked files in $QS differ from $QS_COMMIT"
say "teranode-quickstart at $(git -C "$QS" rev-parse HEAD)"

cd "$QS"
if [ ! -f .env ]; then
	cp .env.example .env
	w() { ./lib/env_writer.sh .env "$1" "$2"; }
	w network regtest
	w SETTINGS_CONTEXT docker.m
	w HOST_IP 127.0.0.1
	w COMPOSE_PROFILES legacy,p2p
	w listen_mode full
	w asset_httpPublicAddress http://asset:8090/api/v1
	w p2p_advertise_addresses /dns4/peer/tcp/9905
	w rpc_user tpnil
	w rpc_pass "$(openssl rand -hex 16)"
	w clientName tpnil-regtest
	w minminingtxfee 0
	w blockmaxsize 4294967296
	w excessiveblocksize 10737418240
	w pruner_block_trigger OnBlockMined
	w POSTGRES_PASSWORD "$(openssl rand -hex 16)"
	# shellcheck source=/dev/null
	source ./lib/admin_key.sh && ensure_admin_api_key .env
	# shellcheck source=/dev/null
	source ./lib/mem_limits.sh && write_mem_limits .env regtest "$(detect_total_ram_gb)"
	say "wrote $QS/.env (regtest, full mode, generated credentials)"
fi
./lib/env_writer.sh .env TERANODE_VERSION "$TERANODE_VERSION"
# shellcheck source=/dev/null
source ./lib/config_perms.sh && fix_config_perms "$QS"

compose() { sudo -n docker compose -f docker-compose.yml -f "$HERE/images.override.yml" "$@"; }
compose pull -q
compose up -d
say "waiting for the RPC service"
user=$(grep -E '^rpc_user=' .env | cut -d= -f2-)
pass=$(grep -E '^rpc_pass=' .env | cut -d= -f2-)
for _ in $(seq 1 60); do
	if curl -sf -m5 --user "$user:$pass" -H 'Content-Type: application/json' \
		-d '{"jsonrpc":"1.0","id":"up","method":"getblockchaininfo","params":[]}' \
		http://127.0.0.1:9292/ >/dev/null; then
		break
	fi
	sleep 5
done
fsm() { sudo -n docker exec blockchain teranode-cli getfsmstate 2>/dev/null | grep -Eio 'state:[[:space:]]*[A-Z]+' | awk '{print toupper($2)}'; }
[ -n "$(fsm)" ] || { compose ps; die "RPC or blockchain service did not come up"; }
# An isolated regtest chain has no peer to catch up from, so the node is moved
# to RUNNING directly; it mines only when asked (generatetoaddress).
if [ "$(fsm)" != RUNNING ]; then
	sudo -n docker exec blockchain teranode-cli setfsmstate --fsmstate RUNNING >/dev/null 2>&1 || true
fi
for _ in $(seq 1 12); do
	if [ "$(fsm)" = RUNNING ]; then
		say "processor ready, FSM RUNNING"
		compose ps --format '{{.Service}} {{.Image}} {{.Status}}'
		exit 0
	fi
	sleep 5
done
die "FSM is $(fsm), not RUNNING"
