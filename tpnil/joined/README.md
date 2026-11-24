# TPNIL joined configuration

This directory builds the `joined` configuration of TPNIL's ITCVE-2 on one
Linux/amd64 host, and qualifies it with TPNIL's own runner. The multicast
delivery fabric sits between unmodified BSV services as a declared transport:

```text
Broadcaster propagation -> arcade-bridge facade -> shard-proxy -> fabric
  -> shard-listener tx lane -> teranode-bridge -> Teranode propagation
Teranode notifications -> teranode-bridge -> shard-proxy -> fabric
  -> shard-listener subtree and block lanes -> arcade-bridge -> Merkle Service
overlay-bridge-a facade -> overlay-host-a, and -> shard-proxy -> fabric
  -> shard-listener beef and header lanes -> overlay-bridge-b -> overlay-host-b
```

## Components

| Layer | Directory | What runs |
| --- | --- | --- |
| Processor | [`processor/`](processor/) | Teranode on an isolated regtest chain, from [teranode-quickstart](https://github.com/bsv-blockchain/teranode-quickstart) at a pinned commit |
| Submission and proofs | [`broadcaster/`](broadcaster/) | BSV Broadcaster and Merkle Service from Broadcaster's own compose file at a pinned commit, with a configuration-only override |
| Fabric and bridges | [`fabric/`](fabric/) | shard-proxy, retry-endpoint (joined to the groups itself), shard-listener with its reference lane, teranode-bridge, arcade-bridge |
| Overlay tier | [`overlay/`](overlay/) | Two overlay hosts ([`../cmd/overlay-host`](../cmd/overlay-host)), their MongoDB store, two overlay bridges, and a header anchor ([`../cmd/header-anchor`](../cmd/header-anchor)) |
| Fixtures | [`../cmd/fixture`](../cmd/fixture) | Regtest keys, funding, Extended Format transactions and proven BEEF |

Every upstream and released image is pinned by digest. The overlay host and
header anchor are built from this repository. The overlay host runs the
[go-overlay-services](https://github.com/bsv-blockchain/go-overlay-services)
engine and MongoDB store unmodified. It serves BRC-22 `/submit` and BRC-24
`/lookup` itself, in the shapes those standards specify.

## Running

Requirements: Linux/amd64, Docker with Compose, `tcpdump`, about 12 GB of
memory free, passwordless `sudo`, TPNIL's Node.js version and Go 1.27.4.

```sh
tpnil/joined/processor/up.sh "$WORK"
tpnil/joined/broadcaster/up.sh standing "$WORK"
tpnil/joined/fabric/up.sh "$WORK"
tpnil/joined/overlay/up.sh "$WORK"

RUNNER_ID="Example Laboratory, operator A. Operator" \
MULTICAST_TEST_COMMIT=<this repository's commit> \
OVERLAY_BRIDGE_COMMIT=<overlay-bridge tpnil-overlay commit> \
  tpnil/joined/qualify.sh "$WORK"
```

`WORK` defaults to `$HOME/tpnil-joined`. `qualify.sh` builds the fixture tool
from this checkout, writes one fixture set,
applies the fabric-only configuration and records it, and runs four plans,
verifying every bundle offline:

| Plan | ISI-5 | Source | What it requires |
| --- | --- | --- | --- |
| Joined path | V008, V009, ready condition | arcade-bridge | Broadcaster's only datahub is the facade; a transaction reaches Teranode through the fabric and is mined; the transaction lane carries the fixture's exact bytes, and the subtree and block lanes the processor's; the block's objects reach Merkle Service through the fabric only; Broadcaster's proof matches the block header |
| Repair under loss | V010 | this repository ([`qualify/`](qualify/)) | After a lossless warmup, with a declared loss on IPv6 UDP into the delivery edge and a lossless trailer, every gap is detected and repaired or reported, each transaction crosses its lane once, and the submission and proof paths still complete |
| Overlay delivery | V011 | overlay-bridge | An object submitted to one host is delivered over the BEEF object plane and admitted by the other; both hosts answer the same lookup; a repeat creates no second admission |
| Restore | Restore condition | arcade-bridge | Broadcaster's discovery and Merkle Service's own libp2p source return |

## V008 by itself, with an outside observer

`qualify-v008.sh` runs one assertion: the transaction lane delivers the
fixture's Extended Format bytes, once, unchanged.

```sh
RUNNER_ID="Example Laboratory, operator A. Operator" \
MULTICAST_TEST_COMMIT=<this repository's commit> \
TPNIL_COMMIT=<p2ppsr/interop commit holding scripts/isi5-observe-tx-lane.mjs> \
  tpnil/joined/qualify-v008.sh "$WORK"
```

| Step | What happens |
| --- | --- |
| Stop | Broadcaster, Merkle Service, the fabric and the bridges stop. The processor stays up, because the fixture spends a coinbase output of its regtest chain |
| Freeze | One transaction is minted and written as `fixture.ef.bin`. The plan binds that file and the laboratory's observer script by SHA-256, and states the byte count and the time |
| Start | The system starts in the fabric-only configuration. The run checks that every container started after the freeze |
| Observe | `tcpdump` captures TCP segments to the processor bridge's lane port on the fabric network's host bridge. `curl` submits the fixture to Broadcaster. The capture runs 20 s past the first delivery |
| Judge | TPNIL's `scripts/isi5-observe-tx-lane.mjs` reassembles the capture and compares it with the byte count and SHA-256 that `wc` and `sha256sum` give for the frozen file. `sha256sum` and `cmp` check the decoded payload again, and the connection's source must be an address of the delivery edge |
| Negative control | The same capture with one payload byte changed must be rejected by the same observer |

| Outcome | Meaning |
| --- | --- |
| PASS | Exactly one delivery, equal to the fixture |
| FAIL | A changed byte, a different transaction, or a second delivery |
| BLOCKED | No capture, no lane packets in it, or a stream with a hole |

No fabric component's own check decides the result;
[`v008/observe.sh`](v008/observe.sh) prints the processor bridge's lane counter
beside it as an accompanying event. The run's output carries the capture and
the decoded payload in base64, so the bundle replays offline:

```sh
tpnil/joined/v008/replay.sh BUNDLE path/to/interop/scripts/isi5-observe-tx-lane.mjs OUT
```

## Notes

- The processor's RPC service is bound to loopback and uses the fixed
  regtest-only credentials `tpnil` / `tpnil-regtest-only`, as TPNIL's own
  Teranode profile does, because plans record them.
- Teranode's `p2p_allow_private_ips` is set, because every libp2p peer of the
  processor is on a private container network.
- The overlay topic is `tm_tpnil_` plus a random suffix chosen once per `WORK`,
  so no two operators share a topic on an object plane.
- Results are `INFRASTRUCTURE_QUALIFICATION_ONLY`. They are not TPNIL
  conformance results and not certifications.
