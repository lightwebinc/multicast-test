#!/bin/bash
# qualify.sh: one TPNIL qualification of the portable joined configuration.
#
#   tpnil/joined/qualify.sh [WORK]
#
# Applies the fabric-only configuration, records it, writes one fixture, runs
# the joined-path plan with TPNIL's runner and verifies the bundle offline; then
# restores the standing configuration and qualifies the restore. The processor,
# Broadcaster and fabric must be up (processor/up.sh, broadcaster/up.sh,
# fabric/up.sh). Needs TPNIL's Node.js and the pinned Go on PATH.
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=${1:-/scratch/tpnil-joined}
TPNIL_COMMIT=${TPNIL_COMMIT:-8d7e42953776bb27b732a460343f38bc07e6ab76}
SOURCE_COMMIT=${SOURCE_COMMIT:-914ec72d990b87eaca9265f2685dd39d7ba891bd}   # arcade-bridge tpnil-joined
RID=${RUNNER_ID:-"Lightweb Inc. laboratory, operator Jeff Harris"}
RUN=$WORK/runs/run-$(date -u +%Y%m%dT%H%M%SZ)
FIX=$WORK/bin/fixture
export TPNIL_RPC_USER=tpnil TPNIL_RPC_PASSWORD=tpnil-regtest-only
GW=$(ip -4 -o addr show docker0 | awk '{print $4}' | cut -d/ -f1)
export PROCESSOR_GATEWAY=$GW

say() { echo "== $(date -u +%H:%M:%S) $*"; }
die() { echo "qualify.sh: $*" >&2; exit 1; }
command -v node >/dev/null && command -v go >/dev/null || die "node and go must be on PATH"

mkdir -p "$RUN" || die "cannot create $RUN"
[ -d "$WORK/tpnil/.git" ] || git clone -q https://github.com/p2ppsr/interop.git "$WORK/tpnil"
git -C "$WORK/tpnil" fetch -q origin && git -C "$WORK/tpnil" -c advice.detachedHead=false checkout -q "$TPNIL_COMMIT" || die "TPNIL checkout"
SRC=$RUN/arcade-bridge
git clone -q https://github.com/lightwebinc/arcade-bridge.git "$SRC" && git -C "$SRC" fetch -q origin tpnil-joined &&
	git -C "$SRC" -c advice.detachedHead=false checkout -q "$SOURCE_COMMIT" || die "arcade-bridge checkout"
say "TPNIL $TPNIL_COMMIT, arcade-bridge $SOURCE_COMMIT, node $(node --version), $(go version | awk '{print $3}')"

say "fixture"
KEY=$WORK/fixtures/key.json
[ -f "$KEY" ] || { mkdir -p "$WORK/fixtures" && "$FIX" keygen -out "$KEY"; }
ADDR=$(python3 -c "import json;print(json.load(open('$KEY'))['address'])")
"$FIX" fund -key "$KEY" -rpc http://127.0.0.1:9292 -blocks 2 | tail -1
"$FIX" ef -key "$KEY" -rpc http://127.0.0.1:9292 -asset "http://$GW:8090" -out "$RUN/fixture.json" | tail -1 || die "no fixture"

say "ready: fabric-only configuration"
"$HERE/broadcaster/up.sh" fabric-only "$WORK" | grep -E "^== " | tail -1
sleep 20
python3 "$HERE/record-profile.py" "$RUN/profile.json" "$WORK" || die "profile not recorded"
python3 "$HERE/make-plans.py" "$RUN" "$SOURCE_COMMIT" "$ADDR" >/dev/null || die "plans not written"

say "qualification: joined path"
node "$WORK/tpnil/bin/tpnil.mjs" run-component-qualification "$RUN/plan.json" "$SRC" "$RUN/bundle" "$RID" > "$RUN/run.json"
node "$WORK/tpnil/bin/tpnil.mjs" verify-component-qualification "$RUN/bundle" > "$RUN/verify.json"
python3 -c "import json;r=json.load(open('$RUN/run.json'));v=json.load(open('$RUN/verify.json'));print('run', r.get('status') or r.get('result'), 'verify', v.get('status') or v.get('valid') or v)" 2>/dev/null || tail -c 400 "$RUN/run.json"

say "restore: standing configuration"
"$HERE/broadcaster/up.sh" standing "$WORK" | grep -E "^== " | tail -1
sleep 30
node "$WORK/tpnil/bin/tpnil.mjs" run-component-qualification "$RUN/restore/plan.json" "$SRC" "$RUN/restore/bundle" "$RID" > "$RUN/restore/run.json"
node "$WORK/tpnil/bin/tpnil.mjs" verify-component-qualification "$RUN/restore/bundle" > "$RUN/restore/verify.json"
python3 -c "import json;r=json.load(open('$RUN/restore/run.json'));v=json.load(open('$RUN/restore/verify.json'));print('run', r.get('status') or r.get('result'), 'verify', v.get('status') or v.get('valid') or v)" 2>/dev/null || tail -c 400 "$RUN/restore/run.json"
say "bundles in $RUN"
