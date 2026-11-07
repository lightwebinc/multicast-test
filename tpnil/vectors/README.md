# Multicast frame conformance vectors

[`multicast-frames-v1.json`](multicast-frames-v1.json) is a conformance corpus
for the wire formats of the multicast delivery fabric, pinned to BRCs commit
`99ee33ea5980313a5c2b01a02a1e42569d981fd6`:

| Vectors | Standards | Covers |
| --- | --- | --- |
| `brc124.frame.example`, `brc124.legacy-frame.example` | BRC-124, BRC-12 | The transaction frame and the legacy frame of the BRC-124 examples, header bytes as printed |
| `brc128.ef-payload` | BRC-128, BRC-30 | An Extended Format payload in an unchanged `0x02` frame, and its detection |
| `brc129.group-index`, `brc124.hashkey` | BRC-129, BRC-124 | Group index and address at four shard widths; the HashKey flow identifier |
| `brc130.two-fragments` | BRC-130 | The two-fragment example, reassembly and the reassembly hash |
| `brc135.header-frame` | BRC-135 | The header-lane frame of a real 80-byte header |
| `brc149.record-frame-delivery.*`, `brc148.plane-group` | BRC-148, BRC-149 | Submission record, BEEF object frame and delivery record for a real Atomic BEEF object, with and without a label topic; the BEEF plane group at zero and four bits |
| `invalid` | BRC-124, BRC-149 | Seven inputs every decoder must reject |

Two oracles check it, and they share no code:

- **Reference**: `go run ./tpnil/cmd/vectors-check tpnil/vectors/multicast-frames-v1.json`
  decodes every vector with the released shard-common decoders. For a
  submission record it applies the grammar and then the object-marker gate,
  which shard-common leaves to its caller and the ingress applies.
- **Independent**: `node tpnil/vectors/verify.mjs tpnil/vectors/multicast-frames-v1.json`
  decodes the same bytes from the BRC layouts alone, with Node.js built-ins
  only, and recomputes every derived value: SHA-256, SHA-256d, XXH64, group
  indices and RFC 5952 addresses.

`go run ./tpnil/cmd/vectors-gen` regenerates the corpus from the reference
encoders and the inputs under [`inputs/`](inputs/): an Extended Format
transaction, an Atomic BEEF object and a block header from the laboratory's
regtest chain. It asserts the header bytes the BRC examples print before it
writes anything.

Copyright 2026 Lightweb Inc. (https://lightweb.net). Licensed under the Apache
License, Version 2.0.
