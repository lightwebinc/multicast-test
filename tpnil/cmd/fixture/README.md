# fixture

`fixture` builds the transaction fixtures that
[TPNIL](https://github.com/p2ppsr/interop) joined-configuration qualifications
read, from an isolated Teranode regtest node. It uses only the
node's JSON-RPC interface and its asset HTTP API, and it builds, signs,
serializes and verifies every transaction with the
[BSV go-sdk](https://github.com/bsv-blockchain/go-sdk).

```sh
go run ./tpnil/cmd/fixture <command> [flags]   # from the root of this checkout
```

## Commands

| Command | What it does | Node interfaces |
| --- | --- | --- |
| `keygen -out FILE` | Writes a new private key and its regtest address to a key file (JSON with `wif` and `address`, mode 0600, never overwritten) and prints the address. | none |
| `fund -key FILE -rpc URL` | Mines `-blocks` blocks to the key's address, by default `-maturity` + 1 (maturity 100), which makes the first coinbase spendable. Prints the number of blocks and the first coinbase txid. | `generatetoaddress`, `getblock` |
| `ef -key FILE -rpc URL -asset URL -out FILE` | Finds the newest mature, unspent coinbase output that pays the key, signs a transaction that spends it to the same address at `-fee` satoshis per kilobyte (default 1000), and writes the Extended Format fixture. `-broadcast` also submits the transaction. | `getblockhash`, `sendrawtransaction`; `/bestblockheader/json`, `/block/<hash>/json`, `/utxos/<txid>/json` |
| `beef -asset URL -txid TXID -out FILE` | Fetches a mined transaction and its merkle proof (BUMP, BRC-74), checks that the proof computes the merkle root in the header of the block that holds the transaction, and writes the Atomic BEEF (BRC-95) that go-sdk builds and verifies against that header. | `/tx/<txid>`, `/merkle_proof/<txid>`, `/txmeta/<txid>/json`, `/header/<hash>/json` |

`-rpc-user` and `-rpc-password` default to `TPNIL_RPC_USER` and
`TPNIL_RPC_PASSWORD`. `-asset` takes the asset API URL; one without a path
gets Teranode's default prefix, `/api/v1`, and the asset paths above are
relative to that prefix. The commands print addresses,
txids and block identifiers, never key material. They report errors on
standard error and exit non-zero. `fixture <command> -h` lists every flag.

## Fixtures

`ef` writes `tpnil-arcade-teranode-fixture@1.0.0`, the schema of the fixture
that TPNIL's Arcade and Teranode qualification reads: `schema`, `txid`,
`efHex` (Extended Format, BRC-30) and `rawHex`, and nothing else, since some
readers decode it strictly. The spent output and the fee are printed. Leave
`-broadcast` off when the qualification submits the transaction itself.

`beef` writes `tpnil-beef-fixture@1.0.0`: `txid`, `beefHex`, `blockHash` and
`blockHeight`. It needs a transaction mined in a block with at least one
other transaction; the asset API has no merkle proof for a coinbase alone in
its block.

Both fixtures are compact JSON with sorted keys.

## Example session

With the node's JSON-RPC interface on port 9292 and its asset API on port
8090, a session that funds a key, spends and mines one of its coinbase
outputs, and proves that spend:

```console
$ export TPNIL_RPC_USER=<rpc-user> TPNIL_RPC_PASSWORD=<rpc-password>
$ RPC=http://localhost:9292 ASSET=http://localhost:8090
$ go run ./tpnil/cmd/fixture keygen -out key.json
<address>
$ go run ./tpnil/cmd/fixture fund -key key.json -rpc $RPC
blocks: 101
first coinbase txid: <coinbase-txid>
$ go run ./tpnil/cmd/fixture ef -key key.json -rpc $RPC -asset $ASSET -out ef.json -broadcast
spent: <mature-coinbase-txid>:0 (coinbase at height <coinbase-height>)
fee: <fee> satoshis
txid: <txid>
broadcast: accepted
$ go run ./tpnil/cmd/fixture fund -key key.json -rpc $RPC -blocks 1
blocks: 1
first coinbase txid: <next-coinbase-txid>
$ go run ./tpnil/cmd/fixture beef -asset $ASSET -txid <txid> -out beef.json
txid: <txid>
block: <block-hash> (height <block-height>)
merkle root: <merkle-root>
```

`beef` needs the block that holds the transaction. If the node had not yet
queued the transaction for mining when `fund -blocks 1` ran, mine another
block.

Copyright 2026 Lightweb Inc. (https://lightweb.net). Licensed under the Apache
License, Version 2.0.
