# Scenario 06 — Functional BRC-128 (EF payload, all shards)

Mirror of scenario 01 with the generator producing **BRC-30 Extended
Format** (BRC-128) payloads instead of BRC-12 raw transactions. The frame
header is identical (BRC-124, 92-byte v2), so proxy/listener/retry forward
frames verbatim without any awareness of payload format.

## Expected

Listener-side metric deltas match scenario 01:

| Listener  | `bsl_frames_forwarded_total\|proto="udp"` Δ | Rationale                       |
|-----------|---------------------------------------------|---------------------------------|
| listener1 | ≈ received                                  | no filter                       |
| listener2 | ≈ received × ⅞                              | shard × subtree filter          |
| listener3 | ≈ received × ⅛                              | only one subtree allowed        |

Additional assertion: `bsl_frames_dropped_total{reason="bad_frame"}` must
remain at zero on every listener — confirming the 92-byte BRC-124 header is
parsed identically regardless of payload contents.

## Run

```bash
bash run.sh
```

Override the generator via env, e.g. `PPS=5000 DURATION=5s bash run.sh`.
