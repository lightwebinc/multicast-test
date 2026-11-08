#!/usr/bin/env python3
"""Write the TPNIL component qualification plans for one portable joined run.

    make-plans.py RUN-DIR SOURCE-COMMIT MINE-ADDRESS

RUN-DIR holds fixture.json and profile.json, which the joined-path plan binds
by digest. Two plans are written: RUN-DIR/plan.json (the joined path in the
fabric-only configuration: ISI-5 V008 and V009 with the ready condition) and
RUN-DIR/restore/plan.json (the restore condition). Every endpoint is on the
qualification host: loopback ports, the processor's Docker gateway, and the
facade by its compose name, which is how Broadcaster's configuration names it.
"""
import hashlib
import json
import os
import sys

run_dir, commit, mine_to = sys.argv[1:4]
GATEWAY = os.environ.get("PROCESSOR_GATEWAY", "172.17.0.1")
SOURCE = {"repository": "https://github.com/lightwebinc/arcade-bridge.git", "commit": commit}
COMMON = {
    "GOTOOLCHAIN": "local",
    "GOFLAGS": "-mod=readonly",
    "TPNIL_ARCADE_URL": "http://127.0.0.1:8080",
    "TPNIL_TERANODE_RPC_URL": "http://127.0.0.1:9292/",
    "TPNIL_TERANODE_RPC_USER": "tpnil",
    "TPNIL_TERANODE_RPC_PASSWORD": "tpnil-regtest-only",
    "TPNIL_MINE_ADDRESS": mine_to,
    "TPNIL_MERKLE_METRICS": "http://127.0.0.1:19187/metrics",
}


def sha256(name):
    with open(os.path.join(run_dir, name), "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


def write(path, plan):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        json.dump(plan, f, indent=2)
        f.write("\n")
    print(path)


write(os.path.join(run_dir, "plan.json"), {
    "schemaVersion": "1.0.0",
    "id": "ISI5-JOINED-PORTABLE-ARCADE-TERANODE-MERKLE",
    "claim": "INFRASTRUCTURE_QUALIFICATION_ONLY",
    "source": SOURCE,
    "environment": dict(COMMON, **{
        "TPNIL_FIXTURE": "${PLAN_ROOT}/fixture.json",
        "TPNIL_PROFILE": "${PLAN_ROOT}/profile.json",
        "TPNIL_FACADE_URL": "http://arcade-bridge:9166",
        "TPNIL_TERANODE_ASSET_URL": f"http://{GATEWAY}:8090/api/v1",
        "TPNIL_ARCADE_BRIDGE_METRICS": "http://127.0.0.1:19167/metrics",
        "TPNIL_TERANODE_BRIDGE_METRICS": "http://127.0.0.1:19146/metrics",
    }),
    "inputs": [
        {"environment": "TPNIL_FIXTURE", "sha256": sha256("fixture.json"),
         "purpose": "Signed Extended Format transaction spending a mature coinbase output of the run's regtest fixture key."},
        {"environment": "TPNIL_PROFILE", "sha256": sha256("profile.json"),
         "purpose": "Fabric-only configuration and component images, recorded from the running containers."},
    ],
    "command": ["go", "test", "-tags", "tpnil", "-run", "^TestTPNIL_FabricJoinedPath$",
                "-count=1", "-v", "-timeout", "15m", "./tests/tpnil"],
    "timeoutSeconds": 1200,
    "requiredOutput": [
        "fixture: EF bytes parse to txid",
        "MTS-01 fabric-only configuration: Broadcaster's sole datahub is the facade",
        "MTS-02 Broadcaster's propagation reached the facade",
        "MTS-03 Teranode accepted transaction",
        "MTS-04 the mined transaction's raw bytes equal the fixture's raw bytes",
        "MTS-05 the reverse path published",
        "with 0 mismatches; Merkle Service received 0 libp2p announcements",
        "MTS-06 Broadcaster returned a mined proof whose computed root",
        "--- PASS: TestTPNIL_FabricJoinedPath",
    ],
})
write(os.path.join(run_dir, "restore", "plan.json"), {
    "schemaVersion": "1.0.0",
    "id": "ISI5-JOINED-PORTABLE-STANDING-RESTORE",
    "claim": "INFRASTRUCTURE_QUALIFICATION_ONLY",
    "source": SOURCE,
    "environment": COMMON,
    "command": ["go", "test", "-tags", "tpnil", "-run", "^TestTPNIL_StandingRestored$",
                "-count=1", "-v", "-timeout", "10m", "./tests/tpnil"],
    "timeoutSeconds": 900,
    "requiredOutput": [
        "MTS-09 the standing configuration is restored",
        "--- PASS: TestTPNIL_StandingRestored",
    ],
})
