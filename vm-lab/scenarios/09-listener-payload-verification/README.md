# Scenario 09 — Listener Payload Hash Verification

Verifies that the `-verify-payload-hash` configuration option correctly validates TxID against SHA256d(payload) on V2 frames and drops mismatches.

## Test Design

1. Temporarily enable `VERIFY_PAYLOAD_HASH=true` on listener1 via systemd EnvironmentFile modification
2. Run subtx-gen with `-corrupt-txid-rate=50` to corrupt 50% of TxIDs (flip random bit)
3. Assert that corrupted frames are dropped and `bsl_frames_invalid_payload_total` increments
4. Assert that only valid frames (50%) are forwarded to egress
5. Restore original config on exit trap

## Expected

Listener1 metric deltas:

| Metric | Expected Δ | Rationale |
|--------|------------|-----------|
| `bsl_frames_received_total` | ≈ 10 000 | all frames received |
| `bsl_frames_invalid_payload_total` | ≈ 5 000 | 50% corrupted TxIDs dropped |
| `bsl_frames_forwarded_total` | ≈ 5 000 | only 50% valid frames forwarded |

Tolerance: ±15% (allowing for random distribution variance).

## Run

```bash
bash run.sh
```

## Cleanup

The scenario automatically restores the original `config.env` and restarts the listener service on exit via trap.
