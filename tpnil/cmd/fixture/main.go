// Command fixture builds the transaction fixtures of TPNIL joined-configuration
// qualifications from an isolated Teranode regtest node, with the BSV go-sdk:
// a key, blocks mined to it, a signed Extended Format spend of a mature
// coinbase output, and an Atomic BEEF of a mined transaction verified against
// its block header.
//
//	go run ./tpnil/cmd/fixture <command> [flags]   (from the root of this checkout)
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

const usage = `Usage: fixture <command> [flags]

Builds TPNIL joined-configuration fixtures from an isolated Teranode regtest
node.

Commands:
  keygen   write a new regtest key file and print its address
  fund     mine blocks to the key's address
  ef       spend a mature coinbase output of the key; write an Extended Format fixture
  beef     prove a mined transaction; write an Atomic BEEF fixture

Run 'fixture <command> -h' for the flags of a command.
`

// errUsage reports a command line that was rejected after its usage was printed.
var errUsage = errors.New("usage")

type command func(args []string, getenv func(string) string, stdout, stderr io.Writer) error

var commands = map[string]command{
	"keygen": keygen,
	"fund":   fund,
	"ef":     ef,
	"beef":   beef,
}

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		_, _ = fmt.Fprint(stderr, usage)
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "fixture: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	err := cmd(args[1:], getenv, stdout, stderr)
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		return 2
	default:
		_, _ = fmt.Fprintf(stderr, "fixture %s: %v\n", args[0], err)
		return 1
	}
}

func keygen(args []string, _ func(string) string, stdout, stderr io.Writer) error {
	fs := newFlagSet("keygen", "-out FILE",
		"Writes a new private key and its regtest address to a key file (JSON: wif,\naddress), created with mode 0600 and never overwritten, and prints the address.", stderr)
	out := fs.String("out", "", "key file to create (required)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *out == "" {
		return usageError(fs, "-out is required")
	}
	kf, err := newKeyFile()
	if err != nil {
		return err
	}
	if err := writeKeyFile(*out, kf); err != nil {
		return err
	}
	return say(stdout, "%s\n", kf.Address)
}

func fund(args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	fs := newFlagSet("fund", "-key FILE -rpc URL [flags]",
		"Mines blocks to the key's address with generatetoaddress, then prints the\nnumber of blocks and the txid of the first block's coinbase.", stderr)
	keyPath := fs.String("key", "", "key file written by keygen (required)")
	var node rpcFlags
	node.register(fs)
	blocks := fs.Int("blocks", 0, "blocks to mine; 0 mines maturity + 1, which makes the first coinbase spendable")
	maturity := fs.Int("maturity", 100, "coinbase maturity of the chain, in blocks")
	timeout := fs.Duration("timeout", 10*time.Minute, "time limit for the node requests")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "key", *keyPath, "rpc", node.url); err != nil {
		return err
	}
	if *blocks < 0 || *maturity < 0 {
		return usageError(fs, "-blocks and -maturity must not be negative")
	}
	n := *blocks
	if n == 0 {
		n = *maturity + 1
	}
	_, addr, err := loadKey(*keyPath)
	if err != nil {
		return err
	}
	lock, err := p2pkh.Lock(addr)
	if err != nil {
		return err
	}
	rpc, err := node.client(getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	hashes, err := rpc.generateToAddress(ctx, n, addr.AddressString)
	if err != nil {
		return err
	}
	if len(hashes) != n {
		return fmt.Errorf("generatetoaddress asked for %d blocks returned %d hashes", n, len(hashes))
	}
	if !isHash(hashes[0]) {
		return fmt.Errorf("generatetoaddress returned block hash %q", hashes[0])
	}
	block, err := rpc.block(ctx, hashes[0])
	if err != nil {
		return err
	}
	cb, err := coinbaseFromBlock(block, hashes[0])
	if err != nil {
		return err
	}
	if len(pays(cb, lock)) == 0 {
		return fmt.Errorf("coinbase %s of block %s does not pay %s", cb.TxID(), hashes[0], addr.AddressString)
	}
	return say(stdout, "blocks: %d\nfirst coinbase txid: %s\n", len(hashes), cb.TxID())
}

func ef(args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	fs := newFlagSet("ef", "-key FILE -rpc URL -asset URL -out FILE [flags]",
		"Finds the newest mature coinbase output that pays the key and is unspent,\n"+
			"signs a transaction that spends it to the same address, and writes its\n"+
			"Extended Format (BRC-30) hex, raw hex and txid as a\n"+
			efSchema+" fixture.", stderr)
	keyPath := fs.String("key", "", "key file written by keygen (required)")
	var node rpcFlags
	node.register(fs)
	assetURL := fs.String("asset", "", "node asset HTTP API URL, for example http://localhost:8090 (required); a URL without a path gets /api/v1")
	out := fs.String("out", "", "fixture file to write (required); with -count above 1, a directory that receives ef-0001.json, ef-0002.json and so on")
	count := fs.Int("count", 1, "fixtures to write, each spending a different mature coinbase output")
	fee := fs.Uint64("fee", 1000, "fee rate, in satoshis per kilobyte")
	maturity := fs.Int("maturity", 100, "coinbase maturity of the chain, in blocks")
	scan := fs.Int("scan", 1000, "most mature blocks to examine, newest first")
	broadcast := fs.Bool("broadcast", false, "submit the transaction with sendrawtransaction before writing the fixture; leave it off when the qualification submits the transaction")
	timeout := fs.Duration("timeout", 10*time.Minute, "time limit for the node requests")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "key", *keyPath, "rpc", node.url, "asset", *assetURL, "out", *out); err != nil {
		return err
	}
	if *maturity < 0 || *scan < 1 || *count < 1 {
		return usageError(fs, "-maturity must not be negative, and -scan and -count must be positive")
	}
	if *count > 1 && *broadcast {
		return usageError(fs, "-broadcast takes a single fixture (-count 1)")
	}
	key, addr, err := loadKey(*keyPath)
	if err != nil {
		return err
	}
	rpc, err := node.client(getenv)
	if err != nil {
		return err
	}
	asset, err := newAssetClient(*assetURL, http.DefaultClient)
	if err != nil {
		return fmt.Errorf("-asset: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if *count > 1 {
		return efMany(ctx, rpc, asset, key, addr, *maturity, *scan, *count, *fee, *out, stdout)
	}
	found, err := findCoinbases(ctx, rpc, asset, addr, *maturity, *scan, 1)
	if err != nil {
		return err
	}
	src, vout, height := found[0].tx, found[0].vout, found[0].height
	tx, err := buildSpend(src, vout, key, addr, *fee)
	if err != nil {
		return err
	}
	fx, err := newEFFixture(tx)
	if err != nil {
		return err
	}
	if *broadcast {
		txid, err := rpc.sendRawTransaction(ctx, fx.RawHex)
		if err != nil {
			return err
		}
		if txid != fx.TxID {
			return fmt.Errorf("sendrawtransaction of %s returned txid %s", fx.TxID, txid)
		}
	}
	if err := writeJSON(*out, fx); err != nil {
		return err
	}
	if err := say(stdout, "spent: %s:%d (coinbase at height %d)\nfee: %d satoshis\ntxid: %s\n", fx.SourceTxID, vout, height, fx.FeeSatoshis, fx.TxID); err != nil || !*broadcast {
		return err
	}
	return say(stdout, "broadcast: accepted\n")
}

// coinbaseOutput is one spendable coinbase output that pays the key.
type coinbaseOutput struct {
	tx     *transaction.Transaction
	vout   uint32
	height uint32
}

// efMany writes count fixtures into the directory out, each spending a
// different mature coinbase output, newest first.
func efMany(ctx context.Context, rpc *rpcClient, asset *assetClient, key *ec.PrivateKey, addr *script.Address, maturity, scan, count int, fee uint64, out string, stdout io.Writer) error {
	found, err := findCoinbases(ctx, rpc, asset, addr, maturity, scan, count)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for i, c := range found {
		tx, err := buildSpend(c.tx, c.vout, key, addr, fee)
		if err != nil {
			return err
		}
		fx, err := newEFFixture(tx)
		if err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(out, fmt.Sprintf("ef-%04d.json", i+1)), fx); err != nil {
			return err
		}
	}
	return say(stdout, "fixtures: %d in %s\n", len(found), out)
}

// findCoinbases returns up to want unspent coinbase outputs that pay addr and
// that a transaction in the next block may spend, newest first: each at a
// height h where h + maturity is at most the tip height + 1, the rule Teranode
// validates. It fails unless it finds want of them within scan blocks.
func findCoinbases(ctx context.Context, rpc *rpcClient, asset *assetClient, addr *script.Address, maturity, scan, want int) ([]coinbaseOutput, error) {
	lock, err := p2pkh.Lock(addr)
	if err != nil {
		return nil, err
	}
	tip, err := asset.bestHeader(ctx)
	if err != nil {
		return nil, err
	}
	newest := min(int64(tip.Height)+1-int64(maturity), int64(tip.Height))
	if newest < 1 {
		return nil, fmt.Errorf("no coinbase is mature at chain height %d with maturity %d", tip.Height, maturity)
	}
	var found []coinbaseOutput
	h := newest
	for ; h >= 1 && newest-h < int64(scan) && len(found) < want; h-- {
		hash, err := rpc.blockHash(ctx, uint32(h))
		if err != nil {
			return nil, err
		}
		cb, err := asset.coinbase(ctx, hash)
		if err != nil {
			return nil, err
		}
		vouts := pays(cb, lock)
		if len(vouts) == 0 {
			continue
		}
		utxos, err := asset.utxos(ctx, cb.TxID().String())
		if err != nil {
			return nil, err
		}
		for _, v := range vouts {
			for _, u := range utxos {
				if u.Vout == v && u.Status == "OK" && len(found) < want {
					found = append(found, coinbaseOutput{tx: cb, vout: v, height: uint32(h)})
				}
			}
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no unspent coinbase output pays %s at heights %d to %d", addr.AddressString, h+1, newest)
	}
	if len(found) < want {
		return nil, fmt.Errorf("found %d unspent coinbase outputs paying %s at heights %d to %d, want %d", len(found), addr.AddressString, h+1, newest, want)
	}
	return found, nil
}

func beef(args []string, _ func(string) string, stdout, stderr io.Writer) error {
	fs := newFlagSet("beef", "-asset URL -txid TXID -out FILE [flags]",
		"Fetches a mined transaction and its merkle proof (BUMP, BRC-74), checks that\n"+
			"the proof computes the merkle root of the block header that holds the\n"+
			"transaction, and writes the Atomic BEEF (BRC-95) that go-sdk builds and\n"+
			"verifies against that header as a "+beefSchema+" fixture.", stderr)
	assetURL := fs.String("asset", "", "node asset HTTP API URL, for example http://localhost:8090 (required); a URL without a path gets /api/v1")
	txidHex := fs.String("txid", "", "txid of the mined transaction (required)")
	out := fs.String("out", "", "fixture file to write (required)")
	timeout := fs.Duration("timeout", 10*time.Minute, "time limit for the node requests")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "asset", *assetURL, "txid", *txidHex, "out", *out); err != nil {
		return err
	}
	txid := strings.ToLower(*txidHex)
	if !isHash(txid) {
		return usageError(fs, "-txid must be 64 hex digits")
	}
	asset, err := newAssetClient(*assetURL, http.DefaultClient)
	if err != nil {
		return fmt.Errorf("-asset: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	tx, err := asset.tx(ctx, txid)
	if err != nil {
		return err
	}
	mp, err := asset.merkleProof(ctx, txid)
	if err != nil {
		return err
	}
	root, err := mp.ComputeRoot(tx.TxID())
	if err != nil {
		return fmt.Errorf("merkle proof of %s: %w", txid, err)
	}
	hdr, err := provingHeader(ctx, asset, txid, mp.BlockHeight, root)
	if err != nil {
		return err
	}
	b, err := buildAtomicBEEF(ctx, tx, mp, hdr.MerkleRoot, hdr.Height)
	if err != nil {
		return err
	}
	fx := beefFixture{BEEFHex: hex.EncodeToString(b), BlockHash: hdr.Hash, BlockHeight: hdr.Height, Schema: beefSchema, TxID: txid}
	if err := writeJSON(*out, fx); err != nil {
		return err
	}
	return say(stdout, "txid: %s\nblock: %s (height %d)\nmerkle root: %s\n", fx.TxID, fx.BlockHash, fx.BlockHeight, hdr.MerkleRoot)
}

// provingHeader returns the header of the block that holds txid at height,
// as the transaction's metadata names it, whose merkle root is root.
func provingHeader(ctx context.Context, asset *assetClient, txid string, height uint32, root *chainhash.Hash) (blockHeader, error) {
	meta, err := asset.txMeta(ctx, txid)
	if err != nil {
		return blockHeader{}, err
	}
	for i := 0; i < len(meta.BlockHashes) && i < len(meta.BlockHeights); i++ {
		hash := meta.BlockHashes[i]
		if meta.BlockHeights[i] != height || !isHash(hash) {
			continue
		}
		hdr, err := asset.header(ctx, hash)
		if err != nil {
			return blockHeader{}, err
		}
		if hdr.Hash == hash && hdr.Height == height && hdr.MerkleRoot == root.String() {
			return hdr, nil
		}
	}
	return blockHeader{}, fmt.Errorf("no block at height %d that holds %s has the merkle root %s its proof computes", height, txid, root)
}

// rpcFlags are the JSON-RPC flags; the user and password fall back to
// TPNIL_RPC_USER and TPNIL_RPC_PASSWORD.
type rpcFlags struct {
	url, user, password string
}

func (f *rpcFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.url, "rpc", "", "node JSON-RPC URL, for example http://localhost:9292 (required)")
	fs.StringVar(&f.user, "rpc-user", "", "JSON-RPC user (default $TPNIL_RPC_USER)")
	fs.StringVar(&f.password, "rpc-password", "", "JSON-RPC password (default $TPNIL_RPC_PASSWORD)")
}

func (f *rpcFlags) client(getenv func(string) string) (*rpcClient, error) {
	if _, err := parseHTTPURL(f.url); err != nil {
		return nil, fmt.Errorf("-rpc: %w", err)
	}
	c := &rpcClient{url: f.url, user: f.user, password: f.password, http: http.DefaultClient}
	if c.user == "" {
		c.user = getenv("TPNIL_RPC_USER")
	}
	if c.password == "" {
		c.password = getenv("TPNIL_RPC_PASSWORD")
	}
	return c, nil
}

func newFlagSet(name, synopsis, about string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("fixture "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "Usage: fixture %s %s\n\n%s\n\nFlags:\n", name, synopsis, about)
		fs.PrintDefaults()
	}
	return fs
}

// parse parses args; the flag package has already printed any error.
func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errUsage
	}
	if fs.NArg() > 0 {
		return usageError(fs, "unexpected argument %q", fs.Arg(0))
	}
	return nil
}

// required takes flag name and value pairs and rejects the first empty one.
func required(fs *flag.FlagSet, pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			return usageError(fs, "-%s is required", pairs[i])
		}
	}
	return nil
}

func usageError(fs *flag.FlagSet, format string, a ...any) error {
	_, _ = fmt.Fprintf(fs.Output(), "%s: %s\n\n", fs.Name(), fmt.Sprintf(format, a...))
	fs.Usage()
	return errUsage
}

// say writes command output; a failed write fails the command.
func say(w io.Writer, format string, a ...any) error {
	_, err := fmt.Fprintf(w, format, a...)
	return err
}
