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
