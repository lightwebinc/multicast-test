#!/bin/bash
# up.sh: bring up BSV Broadcaster and Merkle Service for the ITCVE-2 joined
# configuration from BSV Broadcaster's own compose file at a pinned commit, with
# override.yml (configuration only) on top.
#
#   tpnil/joined/broadcaster/up.sh [standing|fabric-only] [WORK]
#
# standing:    p2p datahub discovery on; Merkle Service bootstraps from the
#              regtest processor's libp2p peer (its own source).
# fabric-only: discovery off and the p2p client stopped, so the facade is
#              Broadcaster's only datahub; Merkle Service's bootstrap peer refuses
#              (loopback port 1), so it holds no peer and its announcements come
#              from the proof bridge alone.
# The processor (processor/up.sh) must be up first. Re-running with the other
# mode recreates only the services whose configuration changed.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
MODE=${1:-standing}
WORK=${2:-$HOME/tpnil-joined}
BR_REPO=https://github.com/bsv-blockchain/arcade.git
BR_COMMIT=e8bc97a5e3c971213a0a0c2933c224816ea7329d   # v0.20.0
BR=$WORK/broadcaster
CFG=$WORK/broadcaster-config
FACADE_URL=${FACADE_URL:-http://arcade-bridge:9166}
REFUSING_PEER=/ip4/127.0.0.1/tcp/1/p2p/12D3KooWAFXWuxgdJoRsaA4J4RRRr8yu6WCrAPf8FaS7UfZg3ceG

say() { echo "== $(date -u +%H:%M:%S) $*"; }
die() { echo "broadcaster/up.sh: $*" >&2; exit 1; }
case $MODE in standing|fabric-only) ;; *) die "mode must be standing or fabric-only" ;; esac

[ -d "$BR/.git" ] || git clone -q "$BR_REPO" "$BR"
git -C "$BR" fetch -q origin
git -C "$BR" -c advice.detachedHead=false checkout -q "$BR_COMMIT"
[ -z "$(git -C "$BR" status --porcelain --untracked-files=no)" ] || die "tracked files in $BR differ from $BR_COMMIT"
say "BSV Broadcaster at $(git -C "$BR" rev-parse HEAD)"

GW=$(ip -4 -o addr show docker0 2>/dev/null | awk '{print $4}' | cut -d/ -f1) || true
[ -n "$GW" ] || die "no docker0 IPv4 address"
PEER_ID=$(sudo -n docker logs peer 2>&1 | grep -oE 'P2P Client created\. ID: [0-9A-Za-z]+' | tail -1 | awk '{print $NF}') || true
[ -n "$PEER_ID" ] || die "processor peer not found; run processor/up.sh first"
TERANODE_PEER=/ip4/$GW/tcp/9905/p2p/$PEER_ID

mkdir -p "$CFG" && chmod 700 "$CFG"
[ -s "$CFG/callback-token" ] || (umask 077 && openssl rand -hex 24 > "$CFG/callback-token")
render() {
	sed -e "s|@CALLBACK_TOKEN@|$(cat "$CFG/callback-token")|" -e "s|@FACADE_URL@|$FACADE_URL|" \
		-e "s|@DISCOVERY@|$1|" -e "s|@TERANODE_PEER@|$TERANODE_PEER|" "$HERE/config.yaml.in"
}
(umask 077 && render true > "$CFG/standing.yaml" && render false > "$CFG/fabric-only.yaml")
chmod 644 "$CFG/standing.yaml" "$CFG/fabric-only.yaml"   # read by the container user

if [ "$MODE" = standing ]; then
	BROADCASTER_CONFIG=$CFG/standing.yaml MERKLE_BOOTSTRAP_PEERS=$TERANODE_PEER
else
	BROADCASTER_CONFIG=$CFG/fabric-only.yaml MERKLE_BOOTSTRAP_PEERS=$REFUSING_PEER
fi
export BROADCASTER_CONFIG MERKLE_BOOTSTRAP_PEERS
compose() {
	sudo -n --preserve-env=BROADCASTER_CONFIG,MERKLE_BOOTSTRAP_PEERS docker compose -p broadcaster \
		--project-directory "$BR" -f "$BR/docker-compose.yaml" -f "$HERE/override.yml" "$@"
}
compose pull -q
compose up -d --remove-orphans
if [ "$MODE" = fabric-only ]; then
	compose stop p2p-client
else
	compose start p2p-client
fi
say "waiting for the API server"
for _ in $(seq 1 60); do
	if curl -sf -m5 -o /dev/null http://127.0.0.1:8080/health; then
		say "broadcaster up ($MODE), processor peer $TERANODE_PEER"
		compose ps --format '{{.Service}} {{.Status}}'
		exit 0
	fi
	sleep 5
done
compose ps
die "API server did not answer within 5 minutes"
