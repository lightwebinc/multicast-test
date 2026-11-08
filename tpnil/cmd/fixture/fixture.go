package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	feemodel "github.com/bsv-blockchain/go-sdk/transaction/fee_model"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	"github.com/bsv-blockchain/go-sdk/util"
)

const (
	// efSchema is the schema of the fixture TPNIL's Arcade and Teranode
	// qualification test reads (infra/adapters/arcade-real-teranode-merkle-test.patch).
	efSchema   = "tpnil-arcade-teranode-fixture@1.0.0"
	beefSchema = "tpnil-beef-fixture@1.0.0"
)

// keyFile is a fixture key: a compressed WIF with the testnet prefix, which
// regtest shares, and its P2PKH address.
type keyFile struct {
	Address string `json:"address"`
	WIF     string `json:"wif"`
}

// efFixture is the tpnil-arcade-teranode-fixture@1.0.0 document. The test
// that consumes it reads schema, txid, efHex and rawHex; the other fields are
// the ones TPNIL's own generator of this schema writes. Fields are declared in
// key order, so the encoding has sorted keys like TPNIL's canonical JSON.
type efFixture struct {
	EFHex             string `json:"efHex"`
	EFSha256          string `json:"efSha256"`
	FeeSatoshis       uint64 `json:"feeSatoshis"`
	RawHex            string `json:"rawHex"`
	RawSha256         string `json:"rawSha256"`
	Schema            string `json:"schema"`
	SourceOutputIndex uint32 `json:"sourceOutputIndex"`
	SourceTxID        string `json:"sourceTxid"`
	TxID              string `json:"txid"`
}

// beefFixture is the tpnil-beef-fixture@1.0.0 document, keys in order.
type beefFixture struct {
	BEEFHex     string `json:"beefHex"`
	BlockHash   string `json:"blockHash"`
	BlockHeight uint32 `json:"blockHeight"`
	Schema      string `json:"schema"`
	TxID        string `json:"txid"`
}

func newKeyFile() (keyFile, error) {
	key, err := ec.NewPrivateKey()
	if err != nil {
		return keyFile{}, err
	}
	addr, err := script.NewAddressFromPublicKey(key.PubKey(), false)
	if err != nil {
		return keyFile{}, err
	}
	return keyFile{Address: addr.AddressString, WIF: key.WifPrefix(byte(ec.TestNet))}, nil
}

// writeKeyFile creates path with mode 0600. It never replaces a file.
func writeKeyFile(path string, kf keyFile) error {
	data, err := encodeJSON(kf)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// loadKey reads a key file and checks that its address belongs to its key.
// Its errors never quote the key.
func loadKey(path string) (*ec.PrivateKey, *script.Address, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var kf keyFile
	if json.Unmarshal(data, &kf) != nil {
		return nil, nil, fmt.Errorf("key file %s is not JSON with wif and address", path)
	}
	key, err := ec.PrivateKeyFromWif(kf.WIF)
	if err != nil {
		return nil, nil, fmt.Errorf("key file %s holds no valid WIF", path)
	}
	addr, err := script.NewAddressFromPublicKey(key.PubKey(), false)
	if err != nil {
		return nil, nil, err
	}
	if addr.AddressString != kf.Address {
		return nil, nil, fmt.Errorf("key file %s: address %q is not the address of its key", path, kf.Address)
	}
	return key, addr, nil
}

// pays lists the outputs of tx that hold satoshis and lock to lock.
func pays(tx *transaction.Transaction, lock *script.Script) []uint32 {
	var vouts []uint32
	for i, out := range tx.Outputs {
		if out.Satoshis > 0 && out.LockingScript != nil && bytes.Equal(*out.LockingScript, *lock) {
			vouts = append(vouts, uint32(i))
		}
	}
	return vouts
}

// buildSpend signs a transaction that spends output vout of source, a P2PKH
// output of key, to addr, less a fee of satsPerKB satoshis per kilobyte of
// go-sdk's size estimate.
func buildSpend(source *transaction.Transaction, vout uint32, key *ec.PrivateKey, addr *script.Address, satsPerKB uint64) (*transaction.Transaction, error) {
	lock, err := p2pkh.Lock(addr)
	if err != nil {
		return nil, err
	}
	if int(vout) >= len(source.Outputs) {
		return nil, fmt.Errorf("transaction %s has no output %d", source.TxID(), vout)
	}
	if out := source.Outputs[vout]; out.LockingScript == nil || !bytes.Equal(*out.LockingScript, *lock) {
		return nil, fmt.Errorf("output %s:%d does not pay %s", source.TxID(), vout, addr.AddressString)
	}
	unlock, err := p2pkh.Unlock(key, nil)
	if err != nil {
		return nil, err
	}
	tx := transaction.NewTransaction()
	tx.AddInputFromTx(source, vout, unlock)
	tx.AddOutput(&transaction.TransactionOutput{LockingScript: lock, Change: true})
	if err := tx.Fee(&feemodel.SatoshisPerKilobyte{Satoshis: satsPerKB}, transaction.ChangeDistributionEqual); err != nil {
		return nil, fmt.Errorf("output %s:%d cannot pay the fee: %w", source.TxID(), vout, err)
	}
	if len(tx.Outputs) != 1 {
		return nil, fmt.Errorf("output %s:%d leaves nothing after the fee", source.TxID(), vout)
	}
	if err := tx.Sign(); err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	return tx, nil
}

// newEFFixture describes a signed one-input transaction.
func newEFFixture(tx *transaction.Transaction) (efFixture, error) {
	ef, err := tx.EF()
	if err != nil {
		return efFixture{}, err
	}
	fee, err := tx.GetFee()
	if err != nil {
		return efFixture{}, err
	}
	raw := tx.Bytes()
	in := tx.Inputs[0]
	return efFixture{
		EFHex:             hex.EncodeToString(ef),
		EFSha256:          sha256Hex(ef),
		FeeSatoshis:       fee,
		RawHex:            hex.EncodeToString(raw),
		RawSha256:         sha256Hex(raw),
		Schema:            efSchema,
		SourceOutputIndex: in.SourceTxOutIndex,
		SourceTxID:        in.SourceTXID.String(),
		TxID:              tx.TxID().String(),
	}, nil
}

// coinbaseFromBlock returns the coinbase of a block serialized as Teranode's
// getblock returns it at verbosity 0: the 80-byte header, then the
// transaction count, the size in bytes and the subtree count as CompactSize
// integers, the subtree hashes, and the coinbase transaction. The header must
// hash to hash.
func coinbaseFromBlock(block []byte, hash string) (*transaction.Transaction, error) {
	if len(block) < 80 {
		return nil, fmt.Errorf("block %s is shorter than a header", hash)
	}
	if got := chainhash.DoubleHashH(block[:80]).String(); got != hash {
		return nil, fmt.Errorf("block %s: header hashes to %s", hash, got)
	}
	r := bytes.NewReader(block[80:])
	var counts [3]util.VarInt // transactions, size in bytes, subtrees
	for i := range counts {
		if _, err := counts[i].ReadFrom(r); err != nil {
			return nil, fmt.Errorf("block %s: %w", hash, err)
		}
	}
	if subtrees := uint64(counts[2]); subtrees > uint64(r.Len())/32 {
		return nil, fmt.Errorf("block %s: subtree list is truncated", hash)
	} else if _, err := r.Seek(int64(subtrees)*32, io.SeekCurrent); err != nil {
		return nil, err
	}
	cb := &transaction.Transaction{}
	if _, err := cb.ReadFrom(r); err != nil {
		return nil, fmt.Errorf("block %s: coinbase: %w", hash, err)
	}
	if !cb.IsCoinbase() {
		return nil, fmt.Errorf("block %s: first transaction is not a coinbase", hash)
	}
	return cb, nil
}

// headerTracker is a go-sdk chain tracker that knows one block header, read
// from the node, and records whether it was consulted.
type headerTracker struct {
	root    string
	height  uint32
	checked bool
}

func (t *headerTracker) IsValidRootForHeight(_ context.Context, root *chainhash.Hash, height uint32) (bool, error) {
	t.checked = true
	return root != nil && root.String() == t.root && height == t.height, nil
}

func (t *headerTracker) CurrentHeight(context.Context) (uint32, error) {
	return t.height, nil
}

// buildAtomicBEEF attaches mp to tx and returns the Atomic BEEF (BRC-95)
// go-sdk serializes, after go-sdk has verified both the merkle path and the
// parsed BEEF against the merkle root and height of the block header.
func buildAtomicBEEF(ctx context.Context, tx *transaction.Transaction, mp *transaction.MerklePath, root string, height uint32) ([]byte, error) {
	txid := tx.TxID()
	if err := flagTxid(mp, txid); err != nil {
		return nil, err
	}
	tracker := &headerTracker{root: root, height: height}
	ok, err := mp.Verify(ctx, txid, tracker)
	if err != nil {
		return nil, fmt.Errorf("merkle path of %s: %w", txid, err)
	}
	if !ok {
		return nil, fmt.Errorf("merkle path of %s does not compute merkle root %s at height %d", txid, root, height)
	}
	if err := tx.AddMerkleProof(mp); err != nil {
		return nil, err
	}
	beef, err := tx.AtomicBEEF(false)
	if err != nil {
		return nil, err
	}
	parsed, subject, err := transaction.NewBeefFromAtomicBytes(beef)
	if err != nil {
		return nil, fmt.Errorf("go-sdk does not parse the Atomic BEEF: %w", err)
	}
	if !subject.Equal(*txid) {
		return nil, fmt.Errorf("atomic BEEF subject is %s, not %s", subject, txid)
	}
	tracker.checked = false
	ok, err = parsed.Verify(ctx, tracker, false)
	if err != nil {
		return nil, err
	}
	if !ok || !tracker.checked {
		return nil, fmt.Errorf("go-sdk does not verify the Atomic BEEF of %s against the block header", txid)
	}
	return beef, nil
}

// flagTxid marks the leaf of txid in the lowest level of mp as a txid, which
// go-sdk's BEEF verification needs to check that leaf against the header.
func flagTxid(mp *transaction.MerklePath, txid *chainhash.Hash) error {
	if len(mp.Path) > 0 {
		for _, leaf := range mp.Path[0] {
			if leaf.Hash != nil && leaf.Hash.Equal(*txid) {
				flag := true
				leaf.Txid = &flag
				return nil
			}
		}
	}
	return fmt.Errorf("merkle path does not hold %s", txid)
}

// encodeJSON is compact JSON and a newline, the form of TPNIL's canonical
// fixture JSON when fields are declared in key order.
func encodeJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeJSON(path string, v any) error {
	data, err := encodeJSON(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// isHash reports whether s is a 64-digit lowercase hex hash.
func isHash(s string) bool {
	if len(s) != 64 || s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
