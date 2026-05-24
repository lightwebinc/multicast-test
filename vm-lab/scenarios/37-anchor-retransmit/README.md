# Scenario 37 — BRC-134 anchor transaction: NACK retransmission

Injects 10% packet loss on all listeners, then sends BRC-134 anchor frames via
UDP. Retry endpoints join `FF0E::B:FFFE` and cache V6 frames. Listeners detect
SeqNum gaps on the anchor flow, dispatch BRC-126 NACKs, and recover dropped
frames via retransmission.

## What it tests

- Gaps on the anchor control flow trigger `bsl_gaps_detected_total{flow="brc134"}`.
- NACKs are dispatched to retry endpoints (`bsl_nacks_dispatched_total{flow="brc134"}`).
- Retry endpoints cache and retransmit anchor frames (`bre_retransmits_total` increases).
- After retransmission, `bsl_gaps_unrecovered_total{flow="brc134"}` remains near zero.

## Pass criteria

| Metric                                             | Expected               |
| -------------------------------------------------- | ---------------------- |
| `bsl_frames_received_total{version="brc134"}`      | ≈ ANCHOR\_COUNT (±15%) |
| `bsl_gaps_detected_total{flow="brc134"}` (sum)     | > 0                    |
| `bsl_gaps_unrecovered_total{flow="brc134"}` (sum)  | ≤ 5% of ANCHOR\_COUNT  |
| `bre_retransmits_total` across all retry endpoints | > 0                    |

## Run

```bash
bash scenarios/37-anchor-retransmit/run.sh
ANCHOR_COUNT=100 LOSS_PCT=15 bash scenarios/37-anchor-retransmit/run.sh
```
