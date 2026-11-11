#!/bin/bash
# qualify.sh: one TPNIL qualification of the portable joined configuration.
#
#   tpnil/joined/qualify.sh [WORK]          (default WORK=$HOME/tpnil-joined)
#
# Applies the fabric-only configuration and records it, writes the fixtures,
# and runs each plan with TPNIL's runner, verifying every bundle offline:
#
#   the joined path (ISI-5 V008, V009 and the ready condition)
#   V010, repair under a declared loss, when MULTICAST_TEST_COMMIT is set
#   V011, overlay delivery, when OVERLAY_BRIDGE_COMMIT is set and the overlay
#     tier is up (overlay/up.sh)
#   the restore condition, after restoring the standing configuration
#
# The processor, Broadcaster and fabric must be up (processor/up.sh,
# broadcaster/up.sh, fabric/up.sh). Needs TPNIL's Node.js and the pinned Go on
# PATH, and passwordless sudo (V010 applies its impairment with tc).
set -uo pipefail

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
WORK=${1:-$HOME/tpnil-joined}
# Absolute, so every path built from it holds wherever a command runs.
WORK=$(mkdir -p -- "$WORK" && CDPATH= cd -- "$WORK" && pwd) || { echo "qualify.sh: cannot use ${1:-$HOME/tpnil-joined} as WORK" >&2; exit 1; }
TPNIL_COMMIT=${TPNIL_COMMIT:-8d7e42953776bb27b732a460343f38bc07e6ab76}
SOURCE_COMMIT=${SOURCE_COMMIT:-2cf627d2cd7d92eeb7fbdca09524e8b06a6dc8a9}   # arcade-bridge v0.1.5: the joined test with lane byte identity
MULTICAST_TEST_COMMIT=${MULTICAST_TEST_COMMIT:-}   # multicast-test commit holding the V010 test
OVERLAY_BRIDGE_COMMIT=${OVERLAY_BRIDGE_COMMIT:-}   # overlay-bridge tpnil-overlay commit holding the V011 test
RID=${RUNNER_ID:?set RUNNER_ID to the runner identity TPNIL records, for example "Example Laboratory, operator A. Operator"}
RUN=$WORK/runs/run-$(date -u +%Y%m%dT%H%M%SZ)
FIX=$WORK/bin/fixture
RPC=http://127.0.0.1:9292
export TPNIL_RPC_USER=tpnil TPNIL_RPC_PASSWORD=tpnil-regtest-only
GW=$(ip -4 -o addr show docker0 2>/dev/null | awk '{print $4}' | cut -d/ -f1) || true
ASSET=http://$GW:8090
export PROCESSOR_GATEWAY=$GW

say() { echo "== $(date -u +%H:%M:%S) $*"; }
die() { echo "qualify.sh: $*" >&2; exit 1; }
command -v node >/dev/null && command -v go >/dev/null || die "node and go must be on PATH"
[ -n "$GW" ] || die "no docker0 IPv4 address; run processor/up.sh first"
V010_COUNT=${V010_COUNT:-116}
if [ -n "$MULTICAST_TEST_COMMIT" ]; then
	# V010 needs 8 lossless fixtures before and after the impaired run, and 10 under it.
	[[ $V010_COUNT =~ ^[0-9]{1,6}$ ]] && [ "$((10#$V010_COUNT))" -ge 26 ] ||
		die "V010_COUNT must be a whole number from 26 to 999999, got '$V010_COUNT'"
	V010_COUNT=$((10#$V010_COUNT))
fi
FAILED=0

# clone REPO COMMIT DIR: a checkout of exactly COMMIT, fetched by its ID so it
# need not be on the default branch.
clone() {
	git clone -q "$1" "$3" && git -C "$3" fetch -q origin "$2" &&
		git -C "$3" -c advice.detachedHead=false checkout -q "$2"
}
# qualify NAME PLAN SOURCE-DIR: one TPNIL run beside its plan, then its offline
# verification.
qualify() {
	local dir
	dir=$(dirname "$2")
	node "$WORK/tpnil/bin/tpnil.mjs" run-component-qualification "$2" "$3" "$dir/bundle" "$RID" > "$dir/run.json" ||
		echo "$1: the run exited non-zero" >&2
	node "$WORK/tpnil/bin/tpnil.mjs" verify-component-qualification "$dir/bundle" > "$dir/verify.json" ||
		echo "$1: the verification exited non-zero" >&2
	# A plan counts only when its bundle verifies offline and passed.
	python3 -c 'import json, sys
try:
    v = json.load(open(sys.argv[2]))
except (OSError, ValueError) as e:
    sys.exit(sys.argv[1] + ": no verification result: " + str(e))
print(sys.argv[1] + ":", v.get("overall"), v.get("assertions"), "assertions, verified", v.get("verified"))
sys.exit(0 if v.get("overall") == "PASS" and v.get("verified") is True else 1)' "$1" "$dir/verify.json" || FAILED=1
}

mkdir -p "$RUN" || die "cannot create $RUN"
[ -d "$WORK/tpnil/.git" ] || git clone -q https://github.com/p2ppsr/interop.git "$WORK/tpnil"
git -C "$WORK/tpnil" fetch -q origin && git -C "$WORK/tpnil" -c advice.detachedHead=false checkout -q "$TPNIL_COMMIT" || die "TPNIL checkout"
SRC=$RUN/arcade-bridge
clone https://github.com/lightwebinc/arcade-bridge.git "$SOURCE_COMMIT" "$SRC" || die "arcade-bridge checkout"
say "TPNIL $TPNIL_COMMIT, arcade-bridge $SOURCE_COMMIT, node $(node --version), $(go version | awk '{print $3}')"

say "fixtures"
# The fixture tool is built from this checkout, so it always matches it.
mkdir -p "$WORK/bin" && (cd "$HERE/../.." && GOWORK=off go build -trimpath -buildvcs=false -o "$FIX" ./tpnil/cmd/fixture) ||
	die "fixture tool not built"
KEY=$WORK/fixtures/key.json
[ -f "$KEY" ] || { mkdir -p "$WORK/fixtures" && "$FIX" keygen -out "$KEY"; }
ADDR=$(python3 -c "import json;print(json.load(open('$KEY'))['address'])")
WANT_V011=0 WANT_V010=0 COUNT=1
[ -n "$OVERLAY_BRIDGE_COMMIT" ] && WANT_V011=1 && COUNT=$((COUNT + 1))
[ -n "$MULTICAST_TEST_COMMIT" ] && WANT_V010=1 && COUNT=$((COUNT + V010_COUNT))
# Mine enough blocks to mature one coinbase per fixture, then write them as one
# set so that no two inputs spend the same output.
"$FIX" fund -key "$KEY" -rpc "$RPC" -blocks "$((COUNT + 101))" | tail -1
"$FIX" ef -key "$KEY" -rpc "$RPC" -asset "$ASSET" -count "$COUNT" -out "$RUN/fixtures-all.json" | tail -1 || die "no fixtures"
python3 "$HERE/split-fixtures.py" "$RUN/fixtures-all.json" "$RUN" "$WANT_V011" "$WANT_V010" || die "fixtures not split"
if [ "$WANT_V011" = 1 ]; then
	[ -s "$WORK/overlay-topic" ] || die "no overlay tier; run overlay/up.sh first"
	OVERLAY_TOPIC=tm_tpnil_$(cat "$WORK/overlay-topic")
	OVERLAY_LOOKUP_SERVICE=ls_tpnil_$(cat "$WORK/overlay-topic")
	export OVERLAY_LOOKUP_SERVICE
	# V011's object is a proven transaction: broadcast it, mine it, prove it.
	curl -sf -m30 --user "$TPNIL_RPC_USER:$TPNIL_RPC_PASSWORD" -H 'Content-Type: application/json' \
		-d "{\"jsonrpc\":\"1.0\",\"id\":\"q\",\"method\":\"sendrawtransaction\",\"params\":[\"$(cat "$RUN/v011/raw.hex")\"]}" \
		"$RPC/" >/dev/null || die "V011 transaction not accepted"
	"$FIX" fund -key "$KEY" -rpc "$RPC" -blocks 1 | tail -1
	sleep 5
	"$FIX" beef -asset "$ASSET" -txid "$(cat "$RUN/v011/txid")" -topic "$OVERLAY_TOPIC" -out "$RUN/v011/beef.json" | head -1 ||
		die "no V011 fixture"
	rm -f "$RUN/v011/raw.hex" "$RUN/v011/txid"
	python3 "$HERE/record-environment.py" "$RUN/v011/environment.json" || die "environment not recorded"
fi

say "ready: fabric-only configuration"
"$HERE/broadcaster/up.sh" fabric-only "$WORK" | grep -E "^== " | tail -1
# From here an early exit must not leave Broadcaster in the fabric-only configuration.
trap '"$HERE/broadcaster/up.sh" standing "$WORK" >/dev/null 2>&1 || echo "qualify.sh: restore the standing configuration with broadcaster/up.sh standing" >&2' EXIT
sleep 20
python3 "$HERE/record-profile.py" "$RUN/profile.json" "$WORK" >/dev/null || die "profile not recorded"
export MULTICAST_TEST_COMMIT OVERLAY_BRIDGE_COMMIT
python3 "$HERE/make-plans.py" "$RUN" "$SOURCE_COMMIT" "$ADDR" >/dev/null || die "plans not written"

say "qualification: joined path"
qualify "joined path" "$RUN/plan.json" "$SRC"
if [ -f "$RUN/v010/plan.json" ]; then
	say "qualification: V010"
	clone https://github.com/lightwebinc/multicast-test.git "$MULTICAST_TEST_COMMIT" "$RUN/v010/multicast-test" || die "multicast-test checkout"
	qualify V010 "$RUN/v010/plan.json" "$RUN/v010/multicast-test"
fi
if [ -f "$RUN/v011/plan.json" ]; then
	say "qualification: V011"
	clone https://github.com/lightwebinc/overlay-bridge.git "$OVERLAY_BRIDGE_COMMIT" "$RUN/v011/overlay-bridge" || die "overlay-bridge checkout"
	qualify V011 "$RUN/v011/plan.json" "$RUN/v011/overlay-bridge"
fi

say "restore: standing configuration"
"$HERE/broadcaster/up.sh" standing "$WORK" | grep -E "^== " | tail -1
trap - EXIT
sleep 30
qualify restore "$RUN/restore/plan.json" "$SRC"
say "bundles in $RUN"
[ "$FAILED" = 0 ] || die "a plan failed or did not verify"
