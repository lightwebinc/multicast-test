#!/usr/bin/env python3
"""Write the TPNIL component qualification plans for one portable joined run.

    make-plans.py RUN-DIR SOURCE-COMMIT MINE-ADDRESS

RUN-DIR holds fixture.json and profile.json, which the joined-path plan binds
by digest. Plans written:

  RUN-DIR/plan.json          the joined path, fabric-only (ISI-5 V008, V009, ready)
  RUN-DIR/restore/plan.json  the restore condition
  RUN-DIR/v010/plan.json     ISI-5 V010, when RUN-DIR/v010/fixtures.json exists
                             and MULTICAST_TEST_COMMIT names the test's commit
  RUN-DIR/v011/plan.json     ISI-5 V011, when RUN-DIR/v011/beef.json and
                             environment.json exist, OVERLAY_BRIDGE_COMMIT names
                             the test's commit and OVERLAY_LOOKUP_SERVICE the
                             hosts' lookup service

Every endpoint is on the qualification host: loopback ports, the processor's
Docker gateway, compose container names, and the facade by its compose name,
which is how Broadcaster's configuration names it.
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
        # Lane byte identity: the transaction lane captured on the fabric
        # network's host bridge, and the proof bridge's retrieval plane.
        "TPNIL_TX_LANE_IFACE": "brtpnil0",
        "TPNIL_TX_LANE_PORT": "8725",
        "TPNIL_TX_LANE_CONTAINER": "tpnil-fabric-teranode-bridge-1",
        "TPNIL_PROOF_BRIDGE_RETRIEVAL_URL": "http://127.0.0.1:19165/api/v1",
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
        "MTS-02 the transaction lane carried the fixture's Extended Format bytes exactly",
        "MTS-03 Teranode accepted transaction",
        "MTS-04 the mined transaction's raw bytes equal the fixture's raw bytes",
        "MTS-05 the reverse path published",
        "with 0 mismatches; Merkle Service received 0 libp2p announcements",
        "MTS-05 the subtree and block lanes delivered the processor's bytes",
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


def sha(path):
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


v010 = os.path.join(run_dir, "v010")
if os.path.exists(os.path.join(v010, "fixtures.json")) and os.environ.get("MULTICAST_TEST_COMMIT"):
    write(os.path.join(v010, "plan.json"), {
        "schemaVersion": "1.0.0",
        "id": "ISI5-JOINED-PORTABLE-V010-REPAIR-UNDER-LOSS",
        "claim": "INFRASTRUCTURE_QUALIFICATION_ONLY",
        "source": {"repository": "https://github.com/lightwebinc/multicast-test.git",
                   "commit": os.environ["MULTICAST_TEST_COMMIT"]},
        "environment": dict(COMMON, **{
            "TPNIL_FIXTURE_SET": "${PLAN_ROOT}/fixtures.json",
            "TPNIL_LISTENER_CONTAINER": "tpnil-fabric-shard-listener-1",
            "TPNIL_RETRY_CONTAINER": "tpnil-fabric-retry-endpoint-1",
            "TPNIL_LISTENER_METRICS": "http://127.0.0.1:19200/metrics",
            "TPNIL_RETRY_METRICS": "http://127.0.0.1:19400/metrics",
            "TPNIL_TERANODE_BRIDGE_METRICS": "http://127.0.0.1:19146/metrics",
            "TPNIL_DECLARED_LOSS_PCT": os.environ.get("DECLARED_LOSS_PCT", "5"),
        }),
        "inputs": [
            {"environment": "TPNIL_FIXTURE_SET", "sha256": sha(os.path.join(v010, "fixtures.json")),
             "purpose": "Signed Extended Format transactions, each spending a different mature coinbase output of the run's regtest fixture key."},
        ],
        "command": ["go", "test", "-tags", "tpnil", "-run", "^TestISI5_V010_RepairUnderLoss$",
                    "-count=1", "-v", "-timeout", "15m", "./tpnil/joined/qualify"],
        "timeoutSeconds": 1200,
        "requiredOutput": [
            "V010 precondition: the retransmission endpoint joins the fabric groups itself",
            "V010 impairment:",
            "V010 every gap on the fabric segment was detected and repaired",
            "+ 0 unrecovered",
            "no transaction had a second financial effect",
            "V010 V008 and V009 hold under the impairment",
            "--- PASS: TestISI5_V010_RepairUnderLoss",
        ],
    })

v011 = os.path.join(run_dir, "v011")
if (os.path.exists(os.path.join(v011, "beef.json")) and os.path.exists(os.path.join(v011, "environment.json"))
        and os.environ.get("OVERLAY_BRIDGE_COMMIT") and os.environ.get("OVERLAY_LOOKUP_SERVICE")):
    write(os.path.join(v011, "plan.json"), {
        "schemaVersion": "1.0.0",
        "id": "ISI5-JOINED-PORTABLE-V011-OVERLAY-DELIVERY",
        "claim": "INFRASTRUCTURE_QUALIFICATION_ONLY",
        "source": {"repository": "https://github.com/lightwebinc/overlay-bridge.git",
                   "commit": os.environ["OVERLAY_BRIDGE_COMMIT"]},
        "environment": {
            "GOTOOLCHAIN": "local",
            "GOFLAGS": "-mod=readonly",
            "TPNIL_BEEF_FIXTURE": "${PLAN_ROOT}/beef.json",
            "TPNIL_ENVIRONMENT": "${PLAN_ROOT}/environment.json",
            "TPNIL_PUBLISHER_FACADE_URL": "http://127.0.0.1:19175",
            "TPNIL_PUBLISHER_ENGINE_URL": "http://127.0.0.1:18281",
            "TPNIL_PUBLISHER_BRIDGE_METRICS": "http://127.0.0.1:19179/metrics",
            "TPNIL_RECEIVER_ENGINE_URL": "http://127.0.0.1:18282",
            "TPNIL_RECEIVER_BRIDGE_METRICS": "http://127.0.0.1:19279/metrics",
            "TPNIL_LOOKUP_SERVICE": os.environ["OVERLAY_LOOKUP_SERVICE"],
        },
        "inputs": [
            {"environment": "TPNIL_BEEF_FIXTURE", "sha256": sha(os.path.join(v011, "beef.json")),
             "purpose": "Atomic BEEF of a transaction mined on the run's regtest chain, for the run's overlay topic."},
            {"environment": "TPNIL_ENVIRONMENT", "sha256": sha(os.path.join(v011, "environment.json")),
             "purpose": "Overlay and fabric component images, recorded from the running containers."},
        ],
        "command": ["go", "test", "-tags", "tpnil", "-run", "^TestTPNIL_OverlayDeliveryContract$",
                    "-count=1", "-v", "-timeout", "10m", "./tests/tpnil"],
        "timeoutSeconds": 900,
        "requiredOutput": [
            "OVL-01 the receiving host's engine holds no output",
            "OVL-02 the publishing host's BRC-22 boundary answered with the topic-keyed admittance of BRC-22",
            "OVL-03 the publishing bridge sent the record",
            "OVL-04 both hosts'",
            "OVL-05 the repeated submission was answered with",
            "so the repeat created no second admission",
            "--- PASS: TestTPNIL_OverlayDeliveryContract",
        ],
    })
