# TPNIL component qualification

This directory lets the Transaction Processing Network Interoperability
Laboratory ([TPNIL](https://github.com/p2ppsr/interop)) runner qualify the
multicast delivery fabric from this harness, under TPNIL's
`INFRASTRUCTURE_QUALIFICATION_ONLY` claim. Each run is described by a TPNIL
component qualification plan that names this repository at an exact commit,
runs one command, and requires named output lines. The runner records the
plan, the runner identity, the complete output and the evaluated result as
content-addressed evidence that anyone can verify offline.

## The closure

The harness compiles every component from a sibling checkout, so a
qualification pins each sibling. [`components.lock.json`](components.lock.json)
lists the released component versions under test, by tag and commit, the Go
toolchain, and each external container image by digest. The scenarios run
against exactly that closure or not at all.

`harness-qualify` keeps preparation separate from the run:

- `prepare` clones each pinned component beside this checkout, writes the
  workspace `go.work` from the lock, and pulls each pinned image by digest.
- `run` verifies every component commit, origin and tracked state, the
  workspace file, the toolchain and the image digests; removes previously
  built harness images so each image is compiled from the pinned sources; and
  runs the selected scenarios as root, because the harness creates network
  namespaces. It prints one line per verified item, the image identifiers it
  built, and its exit code.

## Running a qualification

Requirements: Linux/amd64, Docker, the pinned Go toolchain, passwordless
`sudo`, and TPNIL's Node.js version (`.nvmrc` in the TPNIL repository).

```sh
git clone https://github.com/lightwebinc/multicast-test.git ws/multicast-test
git -C ws/multicast-test checkout <commit named by the plan>
(cd ws/multicast-test && go run ./tpnil/cmd/harness-qualify prepare)

git clone https://github.com/p2ppsr/interop.git tpnil
node tpnil/bin/tpnil.mjs run-component-qualification \
  <plan.json> ws/multicast-test <output-directory> <runner-id>
node tpnil/bin/tpnil.mjs verify-component-qualification <output-directory>
```

A plan's command is the `run` subcommand with the scenario selection, for
example:

```json
["go", "run", "./tpnil/cmd/harness-qualify", "run",
 "-run", "^(TestScenario92_BeefSubmitDeliver|TestScenario94_BeefVersionFilterVerbatim)$",
 "-timeout", "40m"]
```

Its required output includes `components verified: 7 of 7`, the toolchain and
image lines, one `--- PASS:` line per scenario, so that a skipped scenario
fails the plan, and `harness qualification exit 0`.

## What the scenarios establish

| Scenario group | Standards | Contract exercised |
| --- | --- | --- |
| 01, 02, 06, 07, 09 | BRC-124, BRC-128, BRC-129 | Transaction frames delivered to every subscribed shard, shard filtering, Extended Format carriage, mixed formats, payload hash identity at the listener |
| 22 to 25 | BRC-130 | Fragmented transactions reassembled, filtered and hash-verified; loss detected and the partial object abandoned |
| 08, 10, 11, 17, 18, 99 | BRC-126 | Gap detection, NACK, ACK and MISS; the exact identity between sent, delivered and unrecovered frames |
| 89 to 91 | BRC-142 | Bundle coalescing at the origin, decoalescing at the edge, bundle-unit recovery |
| 30 to 37 | BRC-131, BRC-132, BRC-134, BRC-135, BRC-144 | Block announcement, subtree data, anchor frames and the block header lane, with fragment-level recovery |
| 70 to 72 | BRC-139 | Manifest pipeline, successor signal, adoption safety gates |
| 92 to 98, 100 | BRC-148, BRC-149 | BEEF object submission and delivery, topic and version filtering with verbatim carriage, fragmentation and recovery under loss, per-domain coordination, plane independence, multi-topic records |

A qualification establishes that these released components behave as the
scenarios assert in this harness. It is not a TPNIL conformance result and
not a certification.

## Attribution

Copyright 2026 Lightweb Inc. (https://lightweb.net). Licensed under the
Apache License, Version 2.0, as is the rest of this repository. TPNIL is
operated by Peer to Peer Privacy Systems Research LLC; its runner, plan schema
and evidence format are its own and are used here unmodified.
