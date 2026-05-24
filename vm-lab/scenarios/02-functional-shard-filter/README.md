# Scenario 02 — Shard filter + subtree-exclude (listener2)

Same generator stream as scenario 01. Asserts listener2's drop counters
reflect the `shard_include="0,1"` + `subtree_exclude=<idx2>` policy.

## Expected

| Metric | Expected Δ |
|------------------------------------------------------|----------------------|
| `bsl_frames_dropped_total{reason="shard_filter"}` | ≈ 10,000 x 1/2 = 5,000 |
| `bsl_frames_dropped_total{reason="subtree_exclude"}` | ≈ 5,000 x 1/8 ≈ 625 |
| `bsl_frames_forwarded_total` | ≈ 4 375 |

Tolerance: ±10%.

## Run

```bash
bash run.sh
```
