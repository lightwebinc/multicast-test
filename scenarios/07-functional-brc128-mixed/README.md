# Scenario 07 — Functional BRC-128 + BRC-124 coexistence

Generator alternates BRC-12 raw and BRC-30 EF payloads on the same
multicast group (subtx-gen `-payload-format mixed`). Verifies that
proxy/listener/retry treat both payload formats identically because they
share the same BRC-124 92-byte header.

Same ratio assertions as scenarios 01 and 06; additionally asserts that
`bsl_frames_dropped_total{reason="bad_frame"}` is zero on every listener.
