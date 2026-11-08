#!/bin/bash
# up.sh: bring up the overlay tier of the ITCVE-2 joined configuration
# (overlay/compose.yml) and point the delivery edge's beef and header lanes at
# the receiving bridge. The processor, Broadcaster and fabric must be up.
#
#   tpnil/joined/overlay/up.sh [WORK]      (default WORK=$HOME/tpnil-joined)
#
# The topic is tm_tpnil_ plus a random suffix of letters, chosen once per WORK
# and kept in WORK/overlay-topic, so that no two runs or operators share a
# topic on an object plane.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=${1:-$HOME/tpnil-joined}
: "${OVERLAY_BRIDGE_IMAGE:=ghcr.io/lightwebinc/overlay-bridge:0.14.1@sha256:909bade3a95a583b2eb8e3c7e3ce036ad9d8cfd262916d587c26a728b4067494}"

say() { echo "== $(date -u +%H:%M:%S) $*"; }
die() { echo "overlay/up.sh: $*" >&2; exit 1; }

mkdir -p "$WORK"
if [ ! -s "$WORK/overlay-topic" ]; then
	suffix=$(LC_ALL=C tr -dc 'a-z' </dev/urandom | head -c 12)
	echo "$suffix" > "$WORK/overlay-topic"
fi
suffix=$(cat "$WORK/overlay-topic")
OVERLAY_TOPIC=tm_tpnil_$suffix
OVERLAY_LOOKUP_SERVICE=ls_tpnil_$suffix
PROCESSOR_GATEWAY=$(ip -4 -o addr show docker0 | awk '{print $4}' | cut -d/ -f1)
GENESIS_HASH=$(curl -sf -m10 --user tpnil:tpnil-regtest-only -H 'Content-Type: application/json' \
	-d '{"jsonrpc":"1.0","id":"up","method":"getblockhash","params":[0]}' http://127.0.0.1:9292/ |
	python3 -c 'import json,sys;print(json.load(sys.stdin)["result"])') || die "processor RPC does not answer"
export OVERLAY_TOPIC OVERLAY_LOOKUP_SERVICE PROCESSOR_GATEWAY GENESIS_HASH OVERLAY_BRIDGE_IMAGE

say "delivery edge: beef and header lanes to overlay-bridge-b, electing $OVERLAY_TOPIC"
LANE_BEEF=overlay-bridge-b:9171 LANE_HEADER=overlay-bridge-b:9172 BEEF_TOPICS=$OVERLAY_TOPIC \
	"$HERE/../fabric/up.sh" "$WORK" | grep -E "^shard-listener" || die "fabric roll failed"

compose() {
	sudo -n --preserve-env=OVERLAY_TOPIC,OVERLAY_LOOKUP_SERVICE,PROCESSOR_GATEWAY,GENESIS_HASH,OVERLAY_BRIDGE_IMAGE \
		docker compose -f "$HERE/compose.yml" "$@"
}
compose build -q
compose up -d --remove-orphans
say "waiting for both hosts and bridges"
for _ in $(seq 1 60); do
	ok=0
	for u in http://127.0.0.1:18281/listTopicManagers http://127.0.0.1:18282/listTopicManagers \
		http://127.0.0.1:19179/readyz http://127.0.0.1:19279/readyz; do
		curl -sf -m3 -o /dev/null "$u" && ok=$((ok + 1))
	done
	if [ "$ok" = 4 ]; then
		say "overlay tier up: topic $OVERLAY_TOPIC, lookup service $OVERLAY_LOOKUP_SERVICE"
		compose ps --format '{{.Service}} {{.Image}} {{.Status}}'
		exit 0
	fi
	sleep 5
done
compose ps
compose logs --tail 20
die "overlay tier not ready within 5 minutes"
