package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/script/interpreter"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	"github.com/bsv-blockchain/go-sdk/util"
)

// testKey is a fixed key and its regtest address.
func testKey(t testing.TB, seed byte) (*ec.PrivateKey, *script.Address) {
	t.Helper()
	key, _ := ec.PrivateKeyFromBytes(bytes.Repeat([]byte{seed}, 32))
	addr, err := script.NewAddressFromPublicKey(key.PubKey(), false)
	if err != nil {
		t.Fatal(err)
	}
	return key, addr
}

func lockTo(t testing.TB, addr *script.Address) *script.Script {
	t.Helper()
	lock, err := p2pkh.Lock(addr)
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

// coinbaseTx is a coinbase of the given height with the given outputs.
func coinbaseTx(height uint32, outs ...*transaction.TransactionOutput) *transaction.Transaction {
	sig := script.Script(binary.LittleEndian.AppendUint32([]byte{0x04}, height))
	tx := transaction.NewTransaction()
	tx.AddInput(&transaction.TransactionInput{
		SourceTXID:       &chainhash.Hash{},
		SourceTxOutIndex: 0xffffffff,
		SequenceNumber:   0xffffffff,
		UnlockingScript:  &sig,
	})
	for _, o := range outs {
		tx.AddOutput(o)
	}
	return tx
}

func output(sats uint64, lock *script.Script) *transaction.TransactionOutput {
	return &transaction.TransactionOutput{Satoshis: sats, LockingScript: lock}
}

// header80 is a block header with the given parent, merkle root and nonce.
func header80(prev, root *chainhash.Hash, nonce uint32) []byte {
	h := make([]byte, 0, 80)
	h = binary.LittleEndian.AppendUint32(h, 1)
	h = append(h, prev[:]...)
	h = append(h, root[:]...)
	h = binary.LittleEndian.AppendUint32(h, 1_800_000_000+nonce)
	h = binary.LittleEndian.AppendUint32(h, 0x207fffff)
	return binary.LittleEndian.AppendUint32(h, nonce)
}

// teranodeBlock serializes a block as Teranode's getblock does at verbosity 0.
func teranodeBlock(header []byte, txCount uint64, subtrees []chainhash.Hash, cb []byte, height uint32) []byte {
	b := slices.Clone(header)
	b = append(b, util.VarInt(txCount).Bytes()...)
	b = append(b, util.VarInt(uint64(80+len(cb))).Bytes()...)
	b = append(b, util.VarInt(uint64(len(subtrees))).Bytes()...)
	for _, s := range subtrees {
		b = append(b, s[:]...)
	}
	b = append(b, cb...)
	b = append(b, util.VarInt(uint64(height)).Bytes()...)
	return append(b, 0) // no coinbase BUMP
}

func TestBuildSpend(t *testing.T) {
	key, addr := testKey(t, 0x11)
	_, other := testKey(t, 0x22)
	lock, otherLock := lockTo(t, addr), lockTo(t, other)

	// A one-input, one-output P2PKH spend: go-sdk estimates its unlocking
	// script at 106 bytes, so the size it charges for is 191 bytes.
	const estimate = 191
	tests := []struct {
		name    string
		outs    []*transaction.TransactionOutput
		vout    uint32
		rate    uint64
		wantErr string
	}{
		{name: "coinbase output at 1000 sat/kB", outs: []*transaction.TransactionOutput{output(50_0000_0000, lock)}, rate: 1000},
		{name: "second output at 500 sat/kB", outs: []*transaction.TransactionOutput{output(1_0000_0000, otherLock), output(25_0000_0000, lock)}, vout: 1, rate: 500},
		{name: "1 sat/kB rounds up", outs: []*transaction.TransactionOutput{output(1000, lock)}, rate: 1},
		{name: "zero fee", outs: []*transaction.TransactionOutput{output(546, lock)}, rate: 0},
		{name: "output of another key", outs: []*transaction.TransactionOutput{output(50_0000_0000, otherLock)}, rate: 1000, wantErr: "does not pay"},
		{name: "no such output", outs: []*transaction.TransactionOutput{output(50_0000_0000, lock)}, vout: 3, rate: 1000, wantErr: "has no output 3"},
		{name: "fee above the output", outs: []*transaction.TransactionOutput{output(100, lock)}, rate: 1000, wantErr: "cannot pay the fee"},
		{name: "fee equal to the output", outs: []*transaction.TransactionOutput{output(estimate, lock)}, rate: 1000, wantErr: "leaves nothing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := coinbaseTx(7, tc.outs...)
			tx, err := buildSpend(source, tc.vout, key, addr, tc.rate)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			fx, err := newEFFixture(tx)
			if err != nil {
				t.Fatal(err)
			}
			in := tc.outs[tc.vout].Satoshis
			wantFee := (estimate*tc.rate + 999) / 1000
			if fx.FeeSatoshis != wantFee {
				t.Errorf("fee = %d, want %d", fx.FeeSatoshis, wantFee)
			}

			efBytes, err := hex.DecodeString(fx.EFHex)
			if err != nil {
				t.Fatal(err)
			}
			if marker := efBytes[4:10]; !bytes.Equal(marker, []byte{0, 0, 0, 0, 0, 0xef}) {
				t.Fatalf("EF marker = %x", marker)
			}
			decoded, err := transaction.NewTransactionFromHex(fx.EFHex)
			if err != nil {
				t.Fatalf("go-sdk does not decode the EF: %v", err)
			}
			if len(decoded.Inputs) != 1 || len(decoded.Outputs) != 1 {
				t.Fatalf("decoded %d inputs, %d outputs", len(decoded.Inputs), len(decoded.Outputs))
			}
			din := decoded.Inputs[0]
			if sats := din.SourceTxSatoshis(); sats == nil || *sats != in {
				t.Errorf("EF source satoshis = %v, want %d", sats, in)
			}
			if s := din.SourceTxScript(); s == nil || !bytes.Equal(*s, *lock) {
				t.Errorf("EF source script = %v, want %x", s, *lock)
			}
			if din.SourceTXID.String() != source.TxID().String() || din.SourceTxOutIndex != tc.vout {
				t.Errorf("EF spends %s:%d, want %s:%d", din.SourceTXID, din.SourceTxOutIndex, source.TxID(), tc.vout)
			}
			out := decoded.Outputs[0]
			if out.Satoshis != in-wantFee || !bytes.Equal(*out.LockingScript, *lock) {
				t.Errorf("output = %d to %x, want %d to %x", out.Satoshis, *out.LockingScript, in-wantFee, *lock)
			}
			if decoded.TxID().String() != fx.TxID || decoded.Hex() != fx.RawHex {
				t.Error("EF and raw hex describe different transactions")
			}
			raw, err := transaction.NewTransactionFromHex(fx.RawHex)
			if err != nil || raw.TxID().String() != fx.TxID || raw.Inputs[0].SourceTxOutput() != nil {
				t.Errorf("raw hex is not the plain serialization of %s (%v)", fx.TxID, err)
			}
			rawBytes, _ := hex.DecodeString(fx.RawHex)
			if fx.EFSha256 != sha256Hex(efBytes) || fx.RawSha256 != sha256Hex(rawBytes) {
				t.Error("digests do not match the hex")
			}
			if fx.Schema != efSchema || fx.SourceTxID != source.TxID().String() || fx.SourceOutputIndex != tc.vout {
				t.Errorf("fixture identity = %s %s:%d", fx.Schema, fx.SourceTxID, fx.SourceOutputIndex)
			}
			if err := interpreter.NewEngine().Execute(
				interpreter.WithTx(decoded, 0, din.SourceTxOutput()),
				interpreter.WithForkID(),
				interpreter.WithAfterGenesis(),
			); err != nil {
				t.Errorf("signature does not verify: %v", err)
			}
		})
	}
}

func TestKeyFile(t *testing.T) {
	dir := t.TempDir()
	kf, err := newKeyFile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(kf.WIF, "c") || !strings.ContainsAny(kf.Address[:1], "mn") {
		t.Fatalf("not a regtest key: address %s", kf.Address)
	}
	path := filepath.Join(dir, "key.json")
	if err := writeKeyFile(path, kf); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v (%v)", st.Mode(), err)
	}
	if err := writeKeyFile(path, kf); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second write: %v, want an exists error", err)
	}
	key, addr, err := loadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if addr.AddressString != kf.Address || key.WifPrefix(byte(ec.TestNet)) != kf.WIF {
		t.Fatal("loaded key differs from the written one")
	}

	_, other := testKey(t, 0x33)
	mainnet, err := script.NewAddressFromPublicKey(key.PubKey(), true)
	if err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"not json":         "wif=" + kf.WIF,
		"invalid wif":      `{"address":"` + kf.Address + `","wif":"` + kf.WIF[:len(kf.WIF)-1] + `x"}`,
		"foreign address":  `{"address":"` + other.AddressString + `","wif":"` + kf.WIF + `"}`,
		"mainnet address":  `{"address":"` + mainnet.AddressString + `","wif":"` + kf.WIF + `"}`,
		"missing key file": "",
	}
	for name, content := range bad {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
			if content != "" {
				if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := loadKey(p)
			if err == nil {
				t.Fatal("loaded a bad key file")
			}
			if strings.Contains(err.Error(), kf.WIF[2:20]) {
				t.Fatalf("error quotes the key: %v", err)
			}
		})
	}
}

func TestCoinbaseFromBlock(t *testing.T) {
	_, addr := testKey(t, 0x44)
	cb := coinbaseTx(5, output(50_0000_0000, lockTo(t, addr)))
	root := cb.TxID()
	hdr := header80(&chainhash.Hash{1}, root, 5)
	hash := chainhash.DoubleHashH(hdr).String()
	plain := coinbaseTx(5, output(1, lockTo(t, addr)))
	plain.Inputs[0].SourceTXID = &chainhash.Hash{9}

	tests := []struct {
		name    string
		block   []byte
		hash    string
		wantErr string
	}{
		{name: "no subtrees", block: teranodeBlock(hdr, 1, nil, cb.Bytes(), 5), hash: hash},
		{name: "two subtrees", block: teranodeBlock(hdr, 3, []chainhash.Hash{{2}, {3}}, cb.Bytes(), 5), hash: hash},
		{name: "other block", block: teranodeBlock(hdr, 1, nil, cb.Bytes(), 5), hash: strings.Repeat("0", 64), wantErr: "header hashes to " + hash},
		{name: "short", block: hdr[:79], hash: hash, wantErr: "shorter than a header"},
		{name: "truncated subtrees", block: append(append(slices.Clone(hdr), 1, 0x50, 9), make([]byte, 64)...), hash: hash, wantErr: "subtree list is truncated"},
		{name: "truncated coinbase", block: teranodeBlock(hdr, 1, nil, cb.Bytes()[:30], 5)[:80+3+30], hash: hash, wantErr: "coinbase"},
		{name: "not a coinbase", block: teranodeBlock(hdr, 1, nil, plain.Bytes(), 5), hash: hash, wantErr: "not a coinbase"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := coinbaseFromBlock(tc.block, tc.hash)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.TxID().String() != cb.TxID().String() {
				t.Fatalf("coinbase %s, want %s", got.TxID(), cb.TxID())
			}
		})
	}
}

// proof is the BUMP of txids[index] in a block of txids, with the duplicate
// sibling written as an explicit hash, as Teranode writes it; nil for a
// lone transaction, which Teranode cannot encode.
func proof(height uint32, txids []*chainhash.Hash, index int, flag bool) *transaction.MerklePath {
	if len(txids) < 2 {
		return nil
	}
	var path [][]*transaction.PathElement
	level, idx := txids, index
	for len(level) > 1 {
		sib, sibHash := idx^1, level[idx]
		if sib < len(level) {
			sibHash = level[sib]
		}
		elems := []*transaction.PathElement{{Offset: uint64(sib), Hash: sibHash}}
		if len(path) == 0 {
			leaf := &transaction.PathElement{Offset: uint64(idx), Hash: level[idx]}
			if flag {
				leaf.Txid = &flag
			}
			elems = append(elems, leaf)
			slices.SortFunc(elems, func(a, b *transaction.PathElement) int { return cmp.Compare(a.Offset, b.Offset) })
		}
		path = append(path, elems)
		level, idx = parents(level), idx/2
	}
	return transaction.NewMerklePath(height, path)
}

func parents(level []*chainhash.Hash) []*chainhash.Hash {
	var next []*chainhash.Hash
	for i := 0; i < len(level); i += 2 {
		r := level[min(i+1, len(level)-1)]
		next = append(next, transaction.MerkleTreeParent(level[i], r))
	}
	return next
}

func merkleRoot(txids []*chainhash.Hash) *chainhash.Hash {
	for len(txids) > 1 {
		txids = parents(txids)
	}
	return txids[0]
}

func TestBuildAtomicBEEF(t *testing.T) {
	key, addr := testKey(t, 0x55)
	lock := lockTo(t, addr)
	source := coinbaseTx(1, output(50_0000_0000, lock))
	spend, err := buildSpend(source, 0, key, addr, 1000)
	if err != nil {
		t.Fatal(err)
	}
	cb := coinbaseTx(102, output(50_0000_0000, lock))
	third := coinbaseTx(103, output(1, lock)) // any third leaf
	txids := []*chainhash.Hash{cb.TxID(), spend.TxID(), third.TxID()}
	root := merkleRoot(txids).String()

	tests := []struct {
		name    string
		mp      *transaction.MerklePath
		root    string
		height  uint32
		wantErr string
	}{
		{name: "flagged leaf", mp: proof(102, txids, 1, true), root: root, height: 102},
		{name: "unflagged leaf", mp: proof(102, txids, 1, false), root: root, height: 102},
		{name: "other merkle root", mp: proof(102, txids, 1, true), root: cb.TxID().String(), height: 102, wantErr: "does not compute merkle root"},
		{name: "other height", mp: proof(102, txids, 1, true), root: root, height: 101, wantErr: "does not compute merkle root"},
		{name: "proof of another transaction", mp: proof(102, txids, 2, true), root: root, height: 102, wantErr: "does not hold"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The asset API's transaction carries no source transactions.
			tx, err := transaction.NewTransactionFromHex(spend.Hex())
			if err != nil {
				t.Fatal(err)
			}
			b, err := buildAtomicBEEF(context.Background(), tx, tc.mp, tc.root, tc.height)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(b[:4], []byte{1, 1, 1, 1}) {
				t.Fatalf("prefix %x is not Atomic BEEF", b[:4])
			}
			parsed, subject, err := transaction.NewBeefFromAtomicBytes(b)
			if err != nil || subject.String() != spend.TxID().String() {
				t.Fatalf("subject %v (%v)", subject, err)
			}
			got := parsed.FindTransactionByHash(subject)
			if got == nil || got.Hex() != spend.Hex() || got.MerklePath == nil || got.MerklePath.BlockHeight != 102 {
				t.Fatal("BEEF does not carry the proven transaction")
			}
			tracker := &headerTracker{root: root, height: 102}
			if ok, err := parsed.Verify(context.Background(), tracker, false); !ok || err != nil || !tracker.checked {
				t.Fatalf("BEEF does not verify: %v %v", ok, err)
			}
		})
	}
}

// TestFixtureKeyOrder holds the encodings to TPNIL's canonical JSON form:
// compact, keys sorted, one trailing newline.
func TestFixtureKeyOrder(t *testing.T) {
	keyRE := regexp.MustCompile(`"([A-Za-z0-9]+)":`)
	for _, v := range []any{efFixture{}, beefFixture{}, keyFile{}} {
		b, err := encodeJSON(v)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, m := range keyRE.FindAllStringSubmatch(string(b), -1) {
			keys = append(keys, m[1])
		}
		if !slices.IsSorted(keys) || bytes.Count(b, []byte("\n")) != 1 || !json.Valid(b) || bytes.Contains(b, []byte(" ")) {
			t.Errorf("%T encodes as %s", v, b)
		}
	}
}
