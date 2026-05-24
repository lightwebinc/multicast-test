# Expected Metrics — Scenario 09

Listener1 metric deltas after running with 50% TxID corruption:

| Metric | Expected Δ | Tolerance |
|--------|------------|-----------|
| `bsl_frames_received_total` | ≈ 10 000 | — |
| `bsl_frames_invalid_payload_total` | ≈ 5 000 | ±15% |
| `bsl_frames_forwarded_total|proto="udp"` | ≈ 5 000 | ±15% |

## Rationale

- All 10 000 frames are received by listener1
- 50% have corrupted TxIDs (random bit flipped), so they fail SHA256d(payload)==TxID verification
- Corrupted frames are dropped before egress, incrementing `bsl_frames_invalid_payload_total`
- Only the 50% with valid TxIDs are forwarded to downstream
