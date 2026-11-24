#!/bin/bash
# replay.sh: re-run the laboratory's observer offline on the capture a V008
# bundle retains, and repeat the one-byte negative control.
#
#   tpnil/joined/v008/replay.sh BUNDLE OBSERVER [OUT-DIR]
#
# BUNDLE is the directory TPNIL's runner wrote (record.json and objects/).
# OBSERVER is the laboratory's scripts/isi5-observe-tx-lane.mjs. The capture,
# the frozen fixture, the lane port and the bridge addresses all come from the
# bundle; nothing else is read. With OUT-DIR, the capture and the decoded
# payload are left there as files.
set -euo pipefail
BUNDLE=${1:?usage: replay.sh BUNDLE OBSERVER [OUT-DIR]}
OBSERVER=${2:?usage: replay.sh BUNDLE OBSERVER [OUT-DIR]}
OUT=${3:-$(mktemp -d)}
mkdir -p "$OUT"
python3 -I - "$BUNDLE" "$OUT" <<'PY'
import base64, json, os, re, sys
bundle, out = sys.argv[1], sys.argv[2]
obj = lambda h: open(os.path.join(bundle, "objects", h), "rb").read()
record = json.load(open(os.path.join(bundle, "record.json")))
plan = json.loads(obj(record["planHash"]))
observation = json.loads(obj(record["observationHash"]))
names = [i["environment"] for i in plan["inputs"]]
fixture = obj(record["inputHashes"][names.index("TPNIL_TX_FIXTURE_EF")])
capture = base64.b64decode("".join(re.findall(r"^V008-CAPTURE (.*)$", observation["stdout"], re.M)))
hosts = re.search(r"^V008 observer: tcpdump on \S+, filter '(.*)'$", observation["stdout"], re.M)
if hosts is None or not capture:
    sys.exit("replay: the bundle retains no capture")
hosts = hosts.group(1)
open(os.path.join(out, "fixture.ef.bin"), "wb").write(fixture)
open(os.path.join(out, "lane.pcap"), "wb").write(capture)
open(os.path.join(out, "port"), "w").write(plan["environment"]["TPNIL_TX_LANE_PORT"])
open(os.path.join(out, "hosts"), "w").write(" ".join(re.findall(r"dst host (\S+?)\)?(?: |$)", hosts)))
PY
PORT=$(cat "$OUT/port")
HOST_ARGS=()
for h in $(cat "$OUT/hosts"); do HOST_ARGS+=(--host "$h"); done
SHA=$(sha256sum < "$OUT/fixture.ef.bin" | cut -d' ' -f1)
BYTES=$(wc -c < "$OUT/fixture.ef.bin" | tr -d ' ')
rm -f "$OUT/payload.bin" "$OUT/varied.pcap"
node "$OBSERVER" observe --capture "$OUT/lane.pcap" --port "$PORT" "${HOST_ARGS[@]}" \
	--expect-bytes "$BYTES" --expect-sha256 "$SHA" --payload-out "$OUT/payload.bin" | tail -1
node "$OBSERVER" vary --capture "$OUT/lane.pcap" --port "$PORT" "${HOST_ARGS[@]}" --offset "$((BYTES / 2))" --out "$OUT/varied.pcap"
node "$OBSERVER" observe --capture "$OUT/varied.pcap" --port "$PORT" "${HOST_ARGS[@]}" \
	--expect-bytes "$BYTES" --expect-sha256 "$SHA" | tail -1 | sed 's/^/varied: /'
echo "replayed from $BUNDLE; files in $OUT"
