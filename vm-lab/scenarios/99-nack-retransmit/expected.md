# Scenario 99 — NACK / retransmit (active)

## Setup

* Driver: `subtx-gen` on `source` VM with gap injection
  (`-seq-gap-every 200 -seq-gap-size 1 -seq-gap-delay 500ms`).
* Endpoint: single retry endpoint (`retry1`, `fd20::24`),
  multicast cache (in-memory), tier 0, pref 128.
* Listeners (`listener1..3`) configured with
  `RETRY_ENDPOINTS=[fd20::24]:9300`.

## Tunables (env vars on `run.sh`)

| Var | Default | Meaning |
|-----|---------|---------|
| `PPS` | 500 | Frame rate emitted by `subtx-gen` |
| `DURATION` | 15s | Run length |
| `SEQ_GAP_EVERY` | 200 | Inject one gap per N frames |
| `SEQ_GAP_SIZE` | 1 | Skipped seq numbers per gap |
| `SEQ_GAP_DELAY` | 500ms | Delay before late frame is sent (500ms ≫ NACK round-trip ⇒ retry-endpoint cache should already hold the frame when the NACK arrives) |

`SEQ_GAP_DELAY` matters: the late frame must reach the retry-endpoint
*before* the NACK does, otherwise the cache lookup misses. With 50ms it
races; with 500ms the recovery rate is meaningfully positive.

## Pass criteria

| Counter (across listener1..3) | Expectation |
|-------------------------------|-------------|
| `bsl_gaps_detected_total` | > 0 |
| `bsl_nacks_dispatched_total` | > 0 |
| `bsl_gaps_unrecovered_total` | 0 |
| `bre_frames_cached_total` (retry1) | ≈ `subtx-gen sent` |
| `bre_nack_requests_total` (retry1) | ≈ listener `nacks_dispatched` |
| `bre_retransmits_total` (retry1) | > 0 |

The `bsl_gaps_suppressed_total` value is informational only — its ratio
to `gaps_detected` depends on whether the multicast retransmit reaches
the listener inside the gap-tracker's timeout window, which is sensitive
to fabric latency and PPS.

## Last run (lab smoke, 2026-05-04)

```
sent=7441 (15s @ 500pps)
listeners:
  bsl_gaps_detected_total      = 62702
  bsl_nacks_dispatched_total   = 12456
  bsl_gaps_suppressed_total    = 19
  bsl_gaps_unrecovered_total   = 0
retry1:
  bre_frames_cached_total      = 7712
  bre_nack_requests_total      = 12490
  bre_retransmits_total        = 281
```
PASS.
