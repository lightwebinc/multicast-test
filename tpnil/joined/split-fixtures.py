#!/usr/bin/env python3
"""Split one fixture set into the inputs of a run, so that no two spend the
same output.

    split-fixtures.py SET RUN-DIR [V011] [V010]

SET is a tpnil-arcade-teranode-fixtures@1.0.0 document written by
`fixture ef -count N`. The first fixture becomes RUN-DIR/fixture.json (the
joined path). With V011 set to 1, the next one's raw hex is written to
RUN-DIR/v011/raw.hex for the run to broadcast and prove. With V010 set to 1,
the rest become RUN-DIR/v010/fixtures.json, again as a set.
"""
import json
import os
import sys

src, run_dir = sys.argv[1], sys.argv[2]
v011 = len(sys.argv) > 3 and sys.argv[3] == "1"
v010 = len(sys.argv) > 4 and sys.argv[4] == "1"
with open(src) as f:
    s = json.load(f)
fixtures = s["fixtures"]
need = 1 + int(v011) + (2 if v010 else 0)
if len(fixtures) < need:
    sys.exit(f"split-fixtures: {len(fixtures)} fixtures, need at least {need}")


def dump(path, doc):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        json.dump(doc, f, separators=(",", ":"), sort_keys=True)


dump(os.path.join(run_dir, "fixture.json"), fixtures[0])
rest = fixtures[1:]
if v011:
    os.makedirs(os.path.join(run_dir, "v011"), exist_ok=True)
    with open(os.path.join(run_dir, "v011", "raw.hex"), "w") as f:
        f.write(rest[0]["rawHex"] + "\n")
    with open(os.path.join(run_dir, "v011", "txid"), "w") as f:
        f.write(rest[0]["txid"] + "\n")
    rest = rest[1:]
if v010:
    dump(os.path.join(run_dir, "v010", "fixtures.json"), {"schema": s["schema"], "fixtures": rest})
print(f"joined path 1, V011 {int(v011)}, V010 {len(rest) if v010 else 0}")
