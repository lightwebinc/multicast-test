# Scenario 08 — NACK / retransmit with BRC-128 (EF) payloads

Mirror of scenario 99 with `PAYLOAD_FORMAT=brc128`. Confirms the retry
endpoint cache (keyed on the BRC-124 `CurSeq` header field) and the full
NACK + retransmit + cross-endpoint dedup pipeline operate identically on
BRC-30 EF payloads.

## Pre-requisites

Same as scenario 99 (all three retry endpoints running, beacon registry
populated, ~1% listener loss injected by the scenario itself).
