#!/bin/bash
# qualify-v008.sh: one TPNIL qualification of ISI-5 V008's transaction-lane
# byte identity by itself, with the fixture frozen before the system starts
# and the judgment made by an observer outside the fabric's own components.
#
#   tpnil/joined/qualify-v008.sh [WORK]          (default WORK=$HOME/tpnil-joined)
#
# Order of the run:
#
#   1. Stop Broadcaster, Merkle Service, the fabric and the bridges. Only the
#      processor stays up: the fixture spends a coinbase output of its regtest
#      chain, so the chain has to exist before the fixture can.
#   2. Mint one Extended Format transaction, write its bytes to
#      fixture.ef.bin, and write the plan, which binds that file and the
#      laboratory's observer script by SHA-256 and states the byte count and
#      the time of the freeze.
#   3. Start Broadcaster (fabric-only), Merkle Service, the fabric and the
#      bridges.
#   4. Run the plan with TPNIL's runner (v008/observe.sh is its command),
#      verify the bundle offline, and replay the retained capture offline.
#   5. Restore the standing configuration.
#
# The stack must have been brought up once before (processor/up.sh,
# broadcaster/up.sh, fabric/up.sh), so its containers exist and are named.
# Needs TPNIL's Node.js and the pinned Go on PATH, tcpdump, and passwordless
# sudo.
#
#   RUNNER_ID              the runner identity TPNIL records (required)
#   MULTICAST_TEST_COMMIT  this repository's commit holding v008/observe.sh (required)
#   TPNIL_ROOT             a checkout of p2ppsr/interop that holds
#                          scripts/isi5-observe-tx-lane.mjs; default: a clone
#                          at TPNIL_COMMIT under WORK
set -uo pipefail

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
WORK=${1:-$HOME/tpnil-joined}
WORK=$(mkdir -p -- "$WORK" && CDPATH= cd -- "$WORK" && pwd) || { echo "qualify-v008.sh: cannot use ${1:-$HOME/tpnil-joined} as WORK" >&2; exit 1; }
RID=${RUNNER_ID:?set RUNNER_ID to the runner identity TPNIL records, for example "Example Laboratory, operator A. Operator"}
MULTICAST_TEST_COMMIT=${MULTICAST_TEST_COMMIT:?set MULTICAST_TEST_COMMIT to the commit of this repository that holds v008/observe.sh}
TPNIL_COMMIT=${TPNIL_COMMIT:-}
RUN=$WORK/runs/v008-$(date -u +%Y%m%dT%H%M%SZ)
FIX=$WORK/bin/fixture
RPC=http://127.0.0.1:9292
export TPNIL_RPC_USER=tpnil TPNIL_RPC_PASSWORD=tpnil-regtest-only
GW=$(ip -4 -o addr show docker0 2>/dev/null | awk '{print $4}' | cut -d/ -f1) || true
ASSET=http://$GW:8090

say() { echo "== $(date -u +%H:%M:%S) $*"; }
die() { echo "qualify-v008.sh: $*" >&2; exit 1; }
command -v node >/dev/null && command -v go >/dev/null && command -v tcpdump >/dev/null || die "node, go and tcpdump must be on PATH"
[ -n "$GW" ] || die "no docker0 IPv4 address; run processor/up.sh first"

if [ -n "${TPNIL_ROOT:-}" ]; then
	TPNIL_ROOT=$(CDPATH= cd -- "$TPNIL_ROOT" && pwd) || die "TPNIL_ROOT is not a directory"
else
	[ -n "$TPNIL_COMMIT" ] || die "set TPNIL_COMMIT (a p2ppsr/interop commit holding the observer) or TPNIL_ROOT"
	TPNIL_ROOT=$WORK/tpnil
	[ -d "$TPNIL_ROOT/.git" ] || git clone -q https://github.com/p2ppsr/interop.git "$TPNIL_ROOT"
	git -C "$TPNIL_ROOT" fetch -q origin && git -C "$TPNIL_ROOT" -c advice.detachedHead=false checkout -q "$TPNIL_COMMIT" || die "TPNIL checkout"
fi
OBSERVER=$TPNIL_ROOT/scripts/isi5-observe-tx-lane.mjs
[ -f "$OBSERVER" ] || die "no observer at $OBSERVER"

project() { sudo -n docker ps -a --filter "label=com.docker.compose.project=$1" --format '{{.Names}}' | sort | tr '\n' ' '; }
SYSTEM="$(project broadcaster)$(project tpnil-fabric)"
[ -n "$(project broadcaster)" ] && [ -n "$(project tpnil-fabric)" ] ||
	die "bring the stack up once first (broadcaster/up.sh standing, fabric/up.sh)"
SYSTEM=${SYSTEM% }

mkdir -p "$RUN" || die "cannot create $RUN"
say "1. stop the system (the processor stays up)"
sudo -n docker compose -p broadcaster stop >/dev/null 2>&1 || die "Broadcaster did not stop"
sudo -n docker compose -p tpnil-fabric stop >/dev/null 2>&1 || die "the fabric did not stop"
RUNNING=$(sudo -n docker ps --filter label=com.docker.compose.project=broadcaster --filter label=com.docker.compose.project=tpnil-fabric -q | wc -l)
[ "$RUNNING" = 0 ] || die "$RUNNING system containers still run"
# From here an early exit must leave the standing configuration up.
restore() {
	"$HERE/broadcaster/up.sh" standing "$WORK" >/dev/null 2>&1 && "$HERE/fabric/up.sh" "$WORK" >/dev/null 2>&1 ||
		echo "qualify-v008.sh: restore with broadcaster/up.sh standing and fabric/up.sh" >&2
}
trap restore EXIT

say "2. mint and freeze the fixture"
mkdir -p "$WORK/bin" && (cd "$HERE/../.." && GOWORK=off go build -trimpath -buildvcs=false -o "$FIX" ./tpnil/cmd/fixture) ||
	die "fixture tool not built"
KEY=$WORK/fixtures/key.json
[ -f "$KEY" ] || { mkdir -p "$WORK/fixtures" && "$FIX" keygen -out "$KEY"; }
"$FIX" fund -key "$KEY" -rpc "$RPC" -blocks 102 | tail -1
"$FIX" ef -key "$KEY" -rpc "$RPC" -asset "$ASSET" -out "$RUN/fixture.json" | tail -1 || die "no fixture"
python3 -I -c 'import json, sys
f = json.load(open(sys.argv[1]))
open(sys.argv[2], "wb").write(bytes.fromhex(f["efHex"]))
print("txid", f["txid"])' "$RUN/fixture.json" "$RUN/fixture.ef.bin" || die "fixture bytes not written"
chmod 444 "$RUN/fixture.ef.bin"
FROZEN_AT=$(date -u +%FT%TZ)
EF_SHA=$(sha256sum < "$RUN/fixture.ef.bin" | cut -d' ' -f1)
EF_BYTES=$(wc -c < "$RUN/fixture.ef.bin" | tr -d ' ')
OBSERVER_SHA=$(sha256sum < "$OBSERVER" | cut -d' ' -f1)
python3 -I - "$RUN/plan.json" "$MULTICAST_TEST_COMMIT" "$EF_SHA" "$EF_BYTES" "$OBSERVER_SHA" "$FROZEN_AT" "$SYSTEM" <<'PY' || die "plan not written"
import json, sys
out, commit, ef_sha, ef_bytes, observer_sha, frozen_at, system = sys.argv[1:8]
plan = {
    "schemaVersion": "1.0.0",
    "id": "ISI5-JOINED-PORTABLE-V008-TX-LANE-IDENTITY",
    "claim": "INFRASTRUCTURE_QUALIFICATION_ONLY",
    "source": {"repository": "https://github.com/lightwebinc/multicast-test.git", "commit": commit},
    "environment": {
        "TPNIL_TX_FIXTURE_EF": "${PLAN_ROOT}/fixture.ef.bin",
        "TPNIL_FIXTURE_FROZEN_AT": frozen_at,
        "TPNIL_TX_LANE_OBSERVER": "${LAB_ROOT}/scripts/isi5-observe-tx-lane.mjs",
        "TPNIL_BROADCASTER_URL": "http://127.0.0.1:8080",
        "TPNIL_TX_LANE_IFACE": "brtpnil0",
        "TPNIL_TX_LANE_PORT": "8725",
        "TPNIL_TX_LANE_CONTAINER": "tpnil-fabric-teranode-bridge-1",
        "TPNIL_SYSTEM_CONTAINERS": system,
        "TPNIL_TERANODE_BRIDGE_METRICS": "http://127.0.0.1:19146/metrics",
        "TPNIL_SETTLE_SECONDS": "20",
    },
    "inputs": [
        {"environment": "TPNIL_TX_FIXTURE_EF", "sha256": ef_sha,
         "purpose": f"The Extended Format transaction handed to BSV Broadcaster: {ef_bytes} bytes, frozen at {frozen_at}, before the system started."},
        {"environment": "TPNIL_TX_LANE_OBSERVER", "sha256": observer_sha,
         "purpose": "The laboratory's transaction-lane observer, which reassembles the passive capture and compares it with the fixture's byte count and SHA-256."},
    ],
    "command": ["bash", "tpnil/joined/v008/observe.sh"],
    "timeoutSeconds": 600,
    "requiredOutput": [
        f"V008 fixture: {ef_bytes} bytes, sha256 {ef_sha}, frozen at {frozen_at}",
        "V008 system started after the fixture was frozen",
        "V008 submitted the frozen fixture to Broadcaster",
        f"ISI-5 V008 transaction lane: PASS: one delivery, equal to the fixture in byte count and SHA-256; observed {ef_bytes} bytes sha256 {ef_sha}",
        f"V008 evidence payload: {ef_bytes} bytes, sha256 {ef_sha} (sha256sum)",
        "V008 cmp: the decoded payload equals the frozen fixture",
        "V008 negative control: the observer rejected the capture with payload byte",
        "V008 transaction-lane byte identity: PASS",
    ],
}
with open(out, "w") as f:
    json.dump(plan, f, indent=2)
    f.write("\n")
PY
chmod 444 "$RUN/plan.json"
say "frozen at $FROZEN_AT: fixture $EF_BYTES bytes sha256 $EF_SHA; plan sha256 $(sha256sum < "$RUN/plan.json" | cut -d' ' -f1)"
sleep 2

say "3. start the system, fabric-only"
"$HERE/broadcaster/up.sh" fabric-only "$WORK" | grep -E "^== " | tail -1
"$HERE/fabric/up.sh" "$WORK" | tail -5
trap '"$HERE/broadcaster/up.sh" standing "$WORK" >/dev/null 2>&1 || echo "qualify-v008.sh: restore the standing configuration with broadcaster/up.sh standing" >&2' EXIT
sleep 30

say "4. qualification: V008 transaction-lane byte identity"
SRC=$RUN/multicast-test
git clone -q https://github.com/lightwebinc/multicast-test.git "$SRC" && git -C "$SRC" fetch -q origin "$MULTICAST_TEST_COMMIT" &&
	git -C "$SRC" -c advice.detachedHead=false checkout -q "$MULTICAST_TEST_COMMIT" || die "multicast-test checkout"
FAILED=0
node "$TPNIL_ROOT/bin/tpnil.mjs" run-component-qualification "$RUN/plan.json" "$SRC" "$RUN/bundle" "$RID" > "$RUN/run.json" ||
	echo "V008: the run exited non-zero" >&2
node "$TPNIL_ROOT/bin/tpnil.mjs" verify-component-qualification "$RUN/bundle" > "$RUN/verify.json" ||
	echo "V008: the verification exited non-zero" >&2
python3 -I -c 'import json, sys
try:
    v = json.load(open(sys.argv[1]))
except (OSError, ValueError) as e:
    sys.exit("V008: no verification result: " + str(e))
print("V008:", v.get("overall"), v.get("assertions"), "assertions, verified", v.get("verified"))
sys.exit(0 if v.get("overall") == "PASS" and v.get("verified") is True else 1)' "$RUN/verify.json" || FAILED=1
if [ -f "$RUN/bundle/record.json" ]; then
	say "offline replay of the retained capture"
	"$HERE/v008/replay.sh" "$RUN/bundle" "$OBSERVER" "$RUN/replay" | tee "$RUN/replay.txt" || FAILED=1
	grep -q '^ISI-5 V008 transaction lane: PASS: ' "$RUN/replay.txt" && grep -q '^varied: ISI-5 V008 transaction lane: FAIL: ' "$RUN/replay.txt" || FAILED=1
fi

say "5. restore: standing configuration"
"$HERE/broadcaster/up.sh" standing "$WORK" | grep -E "^== " | tail -1
trap - EXIT
say "bundle in $RUN/bundle"
[ "$FAILED" = 0 ] || die "the plan failed, did not verify, or did not replay"
