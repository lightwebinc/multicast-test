# Scenario 10 — Single-Endpoint ACK

**Goal:** verify that when a listener detects a gap and dispatches a NACK to
the sole retry endpoint (`retry1`), the endpoint retransmits and the listener
suppresses the gap.

This is the tightened, per-gap variant of [scenario 99](../99-nack-retransmit/).
Scenario 99 runs at high PPS with aggregate thresholds; scenario 10 uses a
low PPS and a large `SEQ_GAP_EVERY` so only a small number of gaps fire in
the run window, making per-gap recovery observable.

## Topology

```
source ── mc ──▶ proxy ── mc ──▶ listener1..3 (NACK ──▶ retry1)
                                                        │
                                  ◀── mc retransmit ────┘
```

Requires `retry1` to be reachable from listener1..3 on `10.10.10.34:9300`
(ansible inventory: `retry-hosts.yml`).

## Assertions

- `bsl_gaps_detected_total`    ≥ 1  (gap actually fired)
- `bsl_nacks_dispatched_total` ≥ 1  (at least one NACK sent)
- `bre_nack_requests_total`    ≈ `nacks_dispatched` (within 20%)
- `bre_retransmits_total`      ≥ 1
- `bsl_gaps_suppressed_total`  ≥ 1  (at least one gap recovered via ACK)
- `bsl_gaps_unrecovered_total` == 0

> `gaps_detected` and `nacks_dispatched` are **not** 1:1. Most transient
> gaps (caused by out-of-order multicast delivery) close naturally before
> the NACK timer fires, and the listener dedups in-flight NACKs per
> `(groupIdx, PrevSeq)`. The assertion checks that the NACK path is
> exercised, not that every gap produces a NACK.

## Tunables (env)

| Var             | Default | Note                                  |
|-----------------|---------|---------------------------------------|
| `PPS`           | 200     | keep low — want few gaps, not many    |
| `DURATION`      | 10s     |                                       |
| `SEQ_GAP_EVERY` | 500     | frames between deliberate skips       |
| `SEQ_GAP_SIZE`  | 1       | missing seqs per gap                  |
| `SEQ_GAP_DELAY` | 500ms   | ≫ NACK RTT; gives cache time to fill  |

## Run

```bash
bash ~/repo/bitcoin-multicast-test/scenarios/10-single-endpoint-ack/run.sh
```
