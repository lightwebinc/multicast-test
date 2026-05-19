# Scenarios

Each directory is a self-contained end-to-end test. Functional scenarios
target **1000 pps for 10 s** (10 000 frames).

## Index

| Dir                              | Purpose                                                                                            | Primary listener      | Blocked on             |
| -------------------------------- | -------------------------------------------------------------------------------------------------- | --------------------- | ---------------------- |
| `00-firewall/`                   | Positive + negative firewall probes                                                                | all                   | —                      |
| `01-functional-all-shards/`      | All shards, all subtrees                                                                           | listener1             | —                      |
| `02-functional-shard-filter/`    | Half shards + subtree-exclude                                                                      | listener2             | —                      |
| `03-functional-subtree-filter/`  | Single subtree-include                                                                             | listener3             | —                      |
| `04-extended-dashboard/`         | 24h+ 1000 pps for dashboard population                                                             | all                   | —                      |
| `05-mc-egress-bridge/`           | MC egress domain bridge: listener1 re-emits ff05→ff02; listener4 receives                          | listener1 + listener4 | —                      |
| `06-functional-brc128/`          | BRC-128 (BRC-30 EF) payloads, all shards — header parser is payload-agnostic                       | all                   | —                      |
| `07-functional-brc128-mixed/`    | Mixed BRC-124 + BRC-128 payloads on the same multicast group                                       | all                   | —                      |
| `08-nack-retransmit-brc128/`     | NACK/retransmit pipeline with BRC-128 payloads                                                     | all                   | bitcoin-retry-endpoint |
| `10-single-endpoint-ack/`        | Low-PPS per-gap ACK recovery                                                                       | all                   | bitcoin-retry-endpoint |
| `11-permanent-gap-miss/`         | Cache-empty MISS → unrecovered gaps                                                                | all                   | bitcoin-retry-endpoint |
| `12-burst-gap-ratelimit/`        | Multi-frame bursts → rate limiter fires                                                            | all                   | bitcoin-retry-endpoint |
| `13-miss-escalation-tier/`       | 2-hop MISS escalation: retry1→retry2→retry3 ACK                                                    | all                   | bitcoin-retry-endpoint |
| `14-multi-endpoint-ratelimit/`   | Rogue + compromised-listener NACK flood; RL fires on all 3 endpoints                               | all                   | bitcoin-retry-endpoint |
| `15-chain-ratelimit/`            | Fixed non-zero ChainID flood; chain RL fires; ChainID=0 bypasses (orphan)                          | all                   | bitcoin-retry-endpoint |
| `16-group-ratelimit/`            | Dense gap injection; group RL fires post-lookup; ACK still sent on throttle                        | all                   | bitcoin-retry-endpoint |
| `20-subtree-group-announce/`     | BRC-127 dynamic group filtering via SubtreeAnnounce                                                | listener3             | —                      |
| `21-subtree-group-ramp/`         | BRC-127+124 membership ramp over time: dashboard time-series + delivery assertions                 | listener3             | —                      |
| `30-block-announce-delivery/`    | BRC-131 block announce + coinbase frames delivered to all listeners via FF0E::B:FFFE               | all                   | —                      |
| `31-block-announce-retransmit/`  | BRC-131 with 10% loss; NACK recovery via retry endpoints' control-group cache                      | all                   | bitcoin-retry-endpoint |
| `32-subtree-data-delivery/`      | BRC-132 inline SubtreeData frames delivered via FF0X::B:FFFB to all listeners                      | all                   | —                      |
| `33-subtree-data-fragmentation/` | BRC-132 large payload (8 KB) fragmented into BRC-130; listeners reassemble via SubtreeDataCallback | all                   | —                      |
| `34-subtree-data-retransmit/`    | BRC-132 with 10% loss; NACK recovery via retry endpoints caching V5 frames on 0xFFFB               | all                   | bitcoin-retry-endpoint |
| `35-block-header-egress/`        | BRC-131 block headers egressed to listener1 via header_egress; sink counts datagrams               | listener1             | bitcoin-retry-endpoint |
| `99-nack-retransmit/`            | NACK / deferred retransmit (aggregate)                                                             | all                   | bitcoin-retry-endpoint |


## How to add a scenario

1. Create `NN-short-name/` with `README.md`, `run.sh`, `expected.md`.
2. `run.sh` must be idempotent and self-contained: snapshot `/metrics`
   deltas, run the generator, assert pass/fail, exit non-zero on failure.
3. Reference the shared helpers in `scenarios/lib/` rather than duplicating
   curl/jq code.

## Payload format (BRC-124 vs BRC-128)

`scenarios/lib/common.sh` exposes `PAYLOAD_FORMAT` (default `brc124`).
Override to `brc128` or `mixed` to drive `subtx-gen -payload-format`,
which switches the payload between BRC-12 raw transactions and BRC-30
Extended Format (EF) transactions. The frame header is identical in both
cases (BRC-124 92-byte v2), so proxy/listener/retry behaviour is
unchanged — scenarios 06/07/08 exercise this property end-to-end.

```bash
PAYLOAD_FORMAT=brc128 bash scenarios/01-functional-all-shards/run.sh
```

## Rotating the pinned subtree IDs

The listener inventory (`ansible/listener-hosts.yml`) pins two 32-byte
hashes selected from the subtx-gen pool seeded with `multicast-lab-bsv`.

```bash
subtx-gen -subtrees 8 -subtree-seed 'multicast-lab-bsv' -print-subtrees
```

Listener 2 excludes index 2; listener 3 includes only index 5. To rotate,
change the `-subtree-seed` in both `ansible/listener-hosts.yml` and in
`scenarios/lib/common.sh` (SUBTREE_SEED).
