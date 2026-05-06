# Scenarios

Each directory is a self-contained end-to-end test. Functional scenarios
target **1000 pps for 10 s** (10 000 frames).

## Index

| Dir | Purpose | Primary listener | Blocked on |
|---------------------------------|----------------------------------------|------------------|------------------------|
| `00-firewall/` | Positive + negative firewall probes | all | — |
| `01-functional-all-shards/` | All shards, all subtrees | listener1 | — |
| `02-functional-shard-filter/` | Half shards + subtree-exclude | listener2 | — |
| `03-functional-subtree-filter/` | Single subtree-include | listener3 | — |
| `04-extended-dashboard/` | 24h+ 1000 pps for dashboard population | all | — |
| `05-mc-egress-bridge/` | MC egress domain bridge: listener1 re-emits ff05→ff02; listener4 receives | listener1 + listener4 | — |
| `10-single-endpoint-ack/` | Low-PPS per-gap ACK recovery | all | bitcoin-retry-endpoint |
| `11-permanent-gap-miss/` | Cache-empty MISS → unrecovered gaps | all | bitcoin-retry-endpoint |
| `12-burst-gap-ratelimit/` | Multi-frame bursts → rate limiter fires | all | bitcoin-retry-endpoint |
| `13-miss-escalation-tier/` | 2-hop MISS escalation: retry1→retry2→retry3 ACK | all | bitcoin-retry-endpoint |
| `14-multi-endpoint-ratelimit/` | Rogue + compromised-listener NACK flood; RL fires on all 3 endpoints | all | bitcoin-retry-endpoint |
| `99-nack-retransmit/` | NACK / deferred retransmit (aggregate) | all | bitcoin-retry-endpoint |

## How to add a scenario

1. Create `NN-short-name/` with `README.md`, `run.sh`, `expected.md`.
2. `run.sh` must be idempotent and self-contained: snapshot `/metrics`
   deltas, run the generator, assert pass/fail, exit non-zero on failure.
3. Reference the shared helpers in `scenarios/lib/` rather than duplicating
   curl/jq code.

## Rotating the pinned subtree IDs

The listener inventory (`ansible/listener-hosts.yml`) pins two 32-byte
hashes selected from the subtx-gen pool seeded with `lax-lab-2026`.

```bash
subtx-gen -subtrees 8 -subtree-seed 'lax-lab-2026' -print-subtrees
```

Listener 2 excludes index 2; listener 3 includes only index 5. To rotate,
change the `-subtree-seed` in both `ansible/listener-hosts.yml` and in
`scenarios/lib/common.sh` (SUBTREE_SEED).
