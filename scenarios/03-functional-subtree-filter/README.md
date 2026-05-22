# Scenario 03 — Single subtree-include (listener3)

Same stream. Asserts listener3 forwards only frames whose `SubtreeID`
equals the pinned `subtree_include` (pool index 5 under seed
`multicast-lab-bsv`). All other frames are dropped as `subtree_include_miss`.

## Expected

| Metric | Expected Δ |
|-----------------------------------------------------------|----------------------|
| `bsl_frames_forwarded_total` | ≈ 10,000 x 1/8 = 1,250 |
| `bsl_frames_dropped_total{reason="subtree_include_miss"}` | ≈ 10,000 x 7/8 = 8,750 |

Tolerance: ±15% (single-subtree sample is noisier than shard split).

## Run

```bash
bash run.sh
```
