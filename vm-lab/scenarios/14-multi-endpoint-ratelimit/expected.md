# Scenario 14 — Expected Results

## Setup

* Endpoints: retry1 (T0/P128), retry2 (T0/P64), retry3 (T1/P128) — all with
  tight RL: `RL_IP_RATE=5 RL_IP_BURST=3 RL_SEQUENCE_MAX=2 RL_SEQUENCE_WINDOW=10s`.
* Listeners (`listener1..3`) with `retry_endpoints` listing all three.
* Flood sources: source VM (fd20::10, rogue) + listener1 VM (fd20::21,
  compromised), both targeting all three endpoints on port 9300 directly.
* Generator: 500 pps x 15s, gap-every=20, gap-size=2, gap-delay=2s.

## Pass criteria

| Counter | Filter | Endpoint | Expectation |
|---|---|---|---|
| `bsl_gaps_detected_total` | — | listener agg | > 0 |
| `bsl_nacks_dispatched_total` | — | listener agg | > 0 |
| `bre_rate_limit_drops_total` | `level="ip"` | retry1 | > 0 (core) |
| `bre_rate_limit_drops_total` | `level="ip"` | retry2 | > 0 (core) |
| `bre_rate_limit_drops_total` | `level="ip"` | retry3 | > 0 (core) |
| `bsl_gaps_unrecovered_total` | — | listener agg | > 0 (WARN only) |

## Notes

**Why the rogue flood fires `level="ip"` on all three endpoints directly:**
The Python3 script sends to all three NACK ports simultaneously. Each endpoint
sees fd20::10 as the source IP and rate-limits it independently. This is not
an escalation — it's parallel enforcement.

**Why the compromised-listener flood also fires `level="ip"` despite random seqs:**
The per-IP token bucket is the first gate. Even with a fresh LookupSeq every
packet, fd20::21 exhausts its 3-token burst immediately and is then capped at
5 tokens/s. The per-sequence window (`RL_SEQUENCE_MAX=2`) is only reached for
requests that survive the IP gate.

**Why `gaps_unrecovered` may be > 0:**
The long `SEQ_GAP_DELAY=2s` keeps frames absent from the retry endpoint cache.
With RL dropping most NACKs on all three endpoints, listeners exhaust
`NACK_MAX_RETRIES=5` per gap before any endpoint serves a cache hit.
If `gaps_unrecovered=0`, the cache was warm (retransmits self-populated it) or
the RL thresholds need further tightening.

**NACKs reaching retry2 and retry3 (legitimate escalation path):**
A rate-limit drop is a silent drop — no response is sent to the listener. The
listener waits `respTimeout=300ms`, then backs off exponentially before trying
the next endpoint. At 5 retries max, the chain reaches retry2 and potentially
retry3 within the 20s drain window.

## Last run

Not yet executed.
