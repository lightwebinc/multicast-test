#!/bin/bash
# up.sh: bring up the open fabric and the bridges of the ITCVE-2 joined
# configuration (fabric/compose.yml). The processor and Broadcaster must be up.
#
#   tpnil/joined/fabric/up.sh [WORK]
#
# Image references default to the pinned releases below; each can be replaced
# by its environment variable (for example a locally built candidate), and the
# run evidence records the image identifiers actually started.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=${1:-$HOME/tpnil-joined}
: "${SHARD_PROXY_IMAGE:=ghcr.io/lightwebinc/shard-proxy:1.44.0@sha256:5c74b2a949f9997a4db7b65daa740b35c08d15ccbde5feaf7d04769af82b52fa}"
: "${RETRY_ENDPOINT_IMAGE:=ghcr.io/lightwebinc/retry-endpoint:1.17.0@sha256:d558e06874eb61ac2fb17e21937a627af48706daeaa45acb3c09c27f496b0fe7}"
: "${ARCADE_BRIDGE_IMAGE:=ghcr.io/lightwebinc/arcade-bridge:0.1.5@sha256:8f1228ee0e5e74cd2387ff2cd3160b470e1f29311496b5d15eb66e5da791aaff}"
: "${SHARD_LISTENER_IMAGE:=ghcr.io/lightwebinc/shard-listener:1.36.2@sha256:bd306333f8a17c3df2d28ebad7f88830ad0a7b27c3972bf1c7e8cb345b79cdc2}"
: "${TERANODE_BRIDGE_IMAGE:=ghcr.io/lightwebinc/teranode-bridge:0.13.1@sha256:60b9135bfb741a36cd99770cf4e29a803db715dc97bed0bb063685db1c27223c}"
export SHARD_PROXY_IMAGE RETRY_ENDPOINT_IMAGE ARCADE_BRIDGE_IMAGE SHARD_LISTENER_IMAGE TERANODE_BRIDGE_IMAGE

say() { echo "== $(date -u +%H:%M:%S) $*"; }
die() { echo "fabric/up.sh: $*" >&2; exit 1; }

PROCESSOR_GATEWAY=$(ip -4 -o addr show docker0 2>/dev/null | awk '{print $4}' | cut -d/ -f1) || true
[ -n "$PROCESSOR_GATEWAY" ] || die "no docker0 IPv4 address"
export PROCESSOR_GATEWAY
QS_ENV=$WORK/teranode-quickstart/.env
[ -f "$QS_ENV" ] || die "no processor at $WORK; run processor/up.sh first"
SECRETS=$WORK/secrets
mkdir -p "$SECRETS" && chmod 700 "$SECRETS"
(umask 077 && grep -E '^grpc_admin_api_key=' "$QS_ENV" | cut -d= -f2- | tr -d '\n' > "$SECRETS/teranode-admin-key")
chmod 644 "$SECRETS/teranode-admin-key"   # inside a 0700 directory; read by the bridge's container user
export TERANODE_ADMIN_KEY_FILE=$SECRETS/teranode-admin-key

# The fabric segment: an IPv6 bridge with MLD snooping and a querier, as the
# harness builds its own (harness/driver/docker/bridge.go).
if ! sudo -n docker network inspect tpnil-fabric >/dev/null 2>&1; then
	sudo -n docker network create --driver bridge --ipv6 --subnet fd10:2::/64 \
		--opt com.docker.network.bridge.name=brtpnil0 tpnil-fabric >/dev/null
	sudo -n ip link set dev brtpnil0 type bridge mcast_snooping 1
	sudo -n ip link set dev brtpnil0 type bridge mcast_querier 1
	for kv in multicast_query_interval=200 multicast_query_response_interval=100 \
		multicast_startup_query_interval=100 multicast_startup_query_count=2; do
		echo "${kv#*=}" | sudo -n tee "/sys/class/net/brtpnil0/bridge/${kv%=*}" >/dev/null
	done
	say "created fabric segment tpnil-fabric (brtpnil0, fd10:2::/64)"
	sleep 3
fi

compose() {
	sudo -n --preserve-env=SHARD_PROXY_IMAGE,RETRY_ENDPOINT_IMAGE,ARCADE_BRIDGE_IMAGE,SHARD_LISTENER_IMAGE,TERANODE_BRIDGE_IMAGE,PROCESSOR_GATEWAY,TERANODE_ADMIN_KEY_FILE,BEEF_TOPICS,LANE_HEADER,LANE_BEEF \
		docker compose -f "$HERE/compose.yml" "$@"
}
compose up -d --remove-orphans
sleep 15
compose ps --format '{{.Service}} {{.Image}} {{.Status}}'
