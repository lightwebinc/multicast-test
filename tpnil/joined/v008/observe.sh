#!/bin/bash
# observe.sh: ISI-5 V008, the transaction lane's byte identity, observed from
# outside the fabric's own components. This is the command of the plan that
# qualify-v008.sh writes.
#
# The judgment uses only standard tools and the laboratory's observer:
#
#   sha256sum, wc   the expected digest and byte count, from the frozen input
#   tcpdump         a passive capture between the delivery edge and the
#                   processor bridge
#   curl            the submission to BSV Broadcaster
#   TPNIL_TX_LANE_OBSERVER   the laboratory's script: reassembles the capture
#                   and compares it with the expected count and digest
#
# No fabric component's own check decides the result. The processor bridge's
# lane counter is printed beside it as an accompanying event.
#
# Environment (all set by the plan):
#   TPNIL_TX_FIXTURE_EF          the frozen Extended Format transaction, binary
#   TPNIL_FIXTURE_FROZEN_AT      when it was frozen, RFC 3339 UTC
#   TPNIL_TX_LANE_OBSERVER       the observer script
#   TPNIL_BROADCASTER_URL        BSV Broadcaster's API
#   TPNIL_TX_LANE_IFACE          host interface of the fabric network
#   TPNIL_TX_LANE_PORT           the transaction lane's TCP port
#   TPNIL_TX_LANE_CONTAINER      the processor bridge's container
#   TPNIL_SYSTEM_CONTAINERS      containers that must have started after the freeze
#   TPNIL_TERANODE_BRIDGE_METRICS  the processor bridge's metrics URL
#   TPNIL_SETTLE_SECONDS         how long the capture runs on after the first
#                                delivery, to see a second one (default 20)
set -uo pipefail

need() { [ -n "${!1:-}" ] || { echo "observe.sh: $1 is required" >&2; exit 2; }; }
for v in TPNIL_TX_FIXTURE_EF TPNIL_FIXTURE_FROZEN_AT TPNIL_TX_LANE_OBSERVER TPNIL_BROADCASTER_URL \
	TPNIL_TX_LANE_IFACE TPNIL_TX_LANE_PORT TPNIL_TX_LANE_CONTAINER TPNIL_SYSTEM_CONTAINERS TPNIL_TERANODE_BRIDGE_METRICS; do need "$v"; done
SETTLE=${TPNIL_SETTLE_SECONDS:-20}
TMP=$(mktemp -d) || exit 2
CAPTURE=$TMP/lane.pcap
fail() { echo "V008 transaction-lane byte identity: $1: $2"; exit 1; }
stop_capture() {
	# tcpdump runs as root under sudo: signal it by its own capture file. The
	# bracket keeps the pattern from matching the pkill that carries it.
	sudo -n pkill "-$1" -f "tcpdump .*-w [${CAPTURE:0:1}]${CAPTURE:1}" 2>/dev/null
}
cleanup() { stop_capture KILL; sudo -n rm -rf "$TMP"; }
trap cleanup EXIT

# The expectation: from the frozen input alone.
EXPECT_SHA=$(sha256sum < "$TPNIL_TX_FIXTURE_EF" | cut -d' ' -f1)
EXPECT_BYTES=$(wc -c < "$TPNIL_TX_FIXTURE_EF" | tr -d ' ')
echo "V008 fixture: $EXPECT_BYTES bytes, sha256 $EXPECT_SHA, frozen at $TPNIL_FIXTURE_FROZEN_AT"

# The system started after the freeze.
frozen=$(date -u -d "$TPNIL_FIXTURE_FROZEN_AT" +%s) || fail BLOCKED "TPNIL_FIXTURE_FROZEN_AT is not a time"
earliest=
for c in $TPNIL_SYSTEM_CONTAINERS; do
	started=$(sudo -n docker inspect "$c" --format '{{.State.StartedAt}}' 2>/dev/null) || fail BLOCKED "container $c is not running"
	at=$(date -u -d "$started" +%s)
	[ "$at" -gt "$frozen" ] || fail BLOCKED "container $c started $started, not after the fixture was frozen"
	if [ -z "$earliest" ] || [ "$at" -lt "$earliest" ]; then earliest=$at; fi
done
echo "V008 system started after the fixture was frozen: earliest of $(echo "$TPNIL_SYSTEM_CONTAINERS" | wc -w) containers at $(date -u -d "@$earliest" +%FT%TZ)"

# The observation point: TCP segments to the processor bridge's lane port, on
# the host side of the fabric network.
HOSTS=$(sudo -n docker inspect "$TPNIL_TX_LANE_CONTAINER" \
	--format '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{.GlobalIPv6Address}} {{end}}' 2>/dev/null) ||
	fail BLOCKED "container $TPNIL_TX_LANE_CONTAINER not found"
FILTER='' HOST_ARGS=()
for h in $HOSTS; do
	FILTER="${FILTER:+$FILTER or }dst host $h"
	HOST_ARGS+=(--host "$h")
done
[ -n "$FILTER" ] || fail BLOCKED "container $TPNIL_TX_LANE_CONTAINER has no address"
FILTER="tcp dst port $TPNIL_TX_LANE_PORT and ($FILTER)"
sudo -n tcpdump -i "$TPNIL_TX_LANE_IFACE" -U -n -s 0 -Z root -w "$CAPTURE" "$FILTER" 2> "$TMP/tcpdump.err" &
for _ in $(seq 1 100); do
	grep -q "listening on" "$TMP/tcpdump.err" 2>/dev/null && break
	sleep 0.1
done
grep -q "listening on" "$TMP/tcpdump.err" 2>/dev/null || fail BLOCKED "the capture did not start: $(tr '\n' ' ' < "$TMP/tcpdump.err")"
echo "V008 observer: tcpdump on $TPNIL_TX_LANE_IFACE, filter '$FILTER'"

lane_objects() {
	curl -sf -m10 "$TPNIL_TERANODE_BRIDGE_METRICS" |
		awk '/^teranode_bridge_lane_objects_total\{/ && /lane="tx"/ { n += $NF } END { printf "%d\n", n }'
}
BEFORE=$(lane_objects) || fail BLOCKED "the processor bridge's metrics did not answer"

STATUS=$(curl -s -m30 -o "$TMP/submit.out" -w '%{http_code}' -X POST -H 'Content-Type: application/octet-stream' \
	--data-binary "@$TPNIL_TX_FIXTURE_EF" "$TPNIL_BROADCASTER_URL/tx")
case $STATUS in 2??) ;; *) fail FAIL "Broadcaster POST /tx answered HTTP $STATUS: $(head -c 300 "$TMP/submit.out")" ;; esac
echo "V008 submitted the frozen fixture to Broadcaster: HTTP $STATUS"

AFTER=$BEFORE
for _ in $(seq 1 90); do
	AFTER=$(lane_objects) || AFTER=$BEFORE
	[ "$AFTER" -gt "$BEFORE" ] && break
	sleep 1
done
sleep "$SETTLE"
AFTER=$(lane_objects) || AFTER=$BEFORE
stop_capture INT
for _ in $(seq 1 100); do
	stop_capture 0 || break
	sleep 0.1
done
echo "V008 accompanying event: the processor bridge counted $((AFTER - BEFORE)) transaction lane objects while the capture ran (${SETTLE} s past the first)"

sudo -n chmod 644 "$CAPTURE" 2>/dev/null
node "$TPNIL_TX_LANE_OBSERVER" observe --capture "$CAPTURE" --port "$TPNIL_TX_LANE_PORT" "${HOST_ARGS[@]}" \
	--expect-bytes "$EXPECT_BYTES" --expect-sha256 "$EXPECT_SHA" --payload-out "$TMP/payload.bin" > "$TMP/observation.out"
RC=$?
cat "$TMP/observation.out"
[ "$RC" = 0 ] || fail BLOCKED "the observer rejected the capture as invalid evidence (exit $RC)"
VERDICT=$(sed -n 's/^ISI-5 V008 transaction lane: \([A-Z]*\): .*/\1/p' "$TMP/observation.out")

# The evidence itself, so the bundle replays offline.
echo "V008 evidence capture: $(wc -c < "$CAPTURE" | tr -d ' ') bytes, sha256 $(sha256sum < "$CAPTURE" | cut -d' ' -f1), base64:"
base64 -w 76 "$CAPTURE" | sed 's/^/V008-CAPTURE /'
if [ -f "$TMP/payload.bin" ]; then
	# The decoded payload, hashed again with standard tools.
	echo "V008 evidence payload: $(wc -c < "$TMP/payload.bin" | tr -d ' ') bytes, sha256 $(sha256sum < "$TMP/payload.bin" | cut -d' ' -f1) (sha256sum), base64:"
	base64 -w 76 "$TMP/payload.bin" | sed 's/^/V008-PAYLOAD /'
fi
[ "$VERDICT" = PASS ] || fail "${VERDICT:-BLOCKED}" "see the observation above"
cmp -s "$TPNIL_TX_FIXTURE_EF" "$TMP/payload.bin" || fail FAIL "cmp: the decoded payload differs from the frozen fixture"
echo "V008 cmp: the decoded payload equals the frozen fixture"

# Negative control: the same capture with one payload byte changed must be
# rejected by the same observer.
OFFSET=$((EXPECT_BYTES / 2))
node "$TPNIL_TX_LANE_OBSERVER" vary --capture "$CAPTURE" --port "$TPNIL_TX_LANE_PORT" "${HOST_ARGS[@]}" \
	--offset "$OFFSET" --out "$TMP/varied.pcap" || fail BLOCKED "the varied capture was not written"
node "$TPNIL_TX_LANE_OBSERVER" observe --capture "$TMP/varied.pcap" --port "$TPNIL_TX_LANE_PORT" "${HOST_ARGS[@]}" \
	--expect-bytes "$EXPECT_BYTES" --expect-sha256 "$EXPECT_SHA" > "$TMP/varied.out" || fail BLOCKED "the observer did not evaluate the varied capture"
sed 's/^/V008 replay with one byte varied: /' "$TMP/varied.out" | tail -1
grep -q '^ISI-5 V008 transaction lane: FAIL: ' "$TMP/varied.out" ||
	fail BLOCKED "the observer did not reject a capture with payload byte $OFFSET changed"
echo "V008 negative control: the observer rejected the capture with payload byte $OFFSET changed"

echo "V008 transaction-lane byte identity: PASS: one delivery, equal to the frozen fixture in byte count and SHA-256"
