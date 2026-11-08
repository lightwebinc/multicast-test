package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

const (
	maxResponse = 64 << 20
	// assetAttempts bounds the requests made for one asset API path while
	// the node answers 429; its heavy routes are rate limited per client.
	assetAttempts = 6
	maxRetryAfter = 5 * time.Second
)

// rpcClient calls the node's bitcoind-style JSON-RPC interface.
type rpcClient struct {
	url      string
	user     string
	password string
	http     *http.Client
}

type rpcReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *rpcClient) call(ctx context.Context, method string, params []any, result any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "1.0", "id": "tpnil-fixture", "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" || c.password != "" {
		req.SetBasicAuth(c.user, c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("RPC %s: %w", method, err)
	}
	data, err := readBody(resp)
	if err != nil {
		return fmt.Errorf("RPC %s: %w", method, err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("RPC %s: HTTP %d: the node refused the RPC user and password", method, resp.StatusCode)
	}
	var reply rpcReply
	if json.Unmarshal(data, &reply) != nil {
		return fmt.Errorf("RPC %s: HTTP %d: %s", method, resp.StatusCode, excerpt(data))
	}
	if reply.Error != nil {
		return fmt.Errorf("RPC %s: error %d: %s", method, reply.Error.Code, reply.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RPC %s: HTTP %d: %s", method, resp.StatusCode, excerpt(data))
	}
	if len(reply.Result) == 0 || string(reply.Result) == "null" {
		return fmt.Errorf("RPC %s returned no result", method)
	}
	if err := json.Unmarshal(reply.Result, result); err != nil {
		return fmt.Errorf("RPC %s: result: %w", method, err)
	}
	return nil
}

func (c *rpcClient) generateToAddress(ctx context.Context, blocks int, address string) ([]string, error) {
	var hashes []string
	err := c.call(ctx, "generatetoaddress", []any{blocks, address}, &hashes)
	return hashes, err
}

func (c *rpcClient) blockHash(ctx context.Context, height uint32) (string, error) {
	var hash string
	if err := c.call(ctx, "getblockhash", []any{height}, &hash); err != nil {
		return "", err
	}
	if !isHash(hash) {
		return "", fmt.Errorf("RPC getblockhash returned %q", hash)
	}
	return hash, nil
}

// block returns a block in Teranode's serialization (getblock, verbosity 0).
func (c *rpcClient) block(ctx context.Context, hash string) ([]byte, error) {
	var blockHex string
	if err := c.call(ctx, "getblock", []any{hash, 0}, &blockHex); err != nil {
		return nil, err
	}
	b, err := hex.DecodeString(blockHex)
	if err != nil {
		return nil, fmt.Errorf("RPC getblock %s: %w", hash, err)
	}
	return b, nil
}

func (c *rpcClient) sendRawTransaction(ctx context.Context, rawHex string) (string, error) {
	var txid string
	err := c.call(ctx, "sendrawtransaction", []any{rawHex}, &txid)
	return txid, err
}

// assetClient reads the node's asset HTTP API.
type assetClient struct {
	base string // scheme, host and API prefix
	http *http.Client
}

// newAssetClient takes the asset API URL; one without a path gets
// Teranode's default prefix, /api/v1.
func newAssetClient(raw string, hc *http.Client) (*assetClient, error) {
	u, err := parseHTTPURL(raw)
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/api/v1"
	}
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	return &assetClient{base: u.String(), http: hc}, nil
}

// get returns the body of a 200 response to GET path, waiting out the
// Retry-After of a 429 a bounded number of times.
func (a *assetClient) get(ctx context.Context, path string) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
		if err != nil {
			return nil, err
		}
		resp, err := a.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("asset GET %s: %w", path, err)
		}
		data, err := readBody(resp)
		if err != nil {
			return nil, fmt.Errorf("asset GET %s: %w", path, err)
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			return data, nil
		case resp.StatusCode == http.StatusTooManyRequests && attempt < assetAttempts:
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryAfter(resp.Header.Get("Retry-After"))):
			}
		default:
			return nil, fmt.Errorf("asset GET %s: HTTP %d: %s", path, resp.StatusCode, excerpt(data))
		}
	}
}

func (a *assetClient) getJSON(ctx context.Context, path string, v any) error {
	data, err := a.get(ctx, path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("asset GET %s: %w", path, err)
	}
	return nil
}

// blockHeader is the part of the asset API's block header JSON
// (/header/<hash>/json, /bestblockheader/json) this tool reads.
type blockHeader struct {
	Hash       string `json:"hash"`
	Height     uint32 `json:"height"`
	MerkleRoot string `json:"merkleroot"`
}

func (a *assetClient) bestHeader(ctx context.Context) (blockHeader, error) {
	var h blockHeader
	err := a.getJSON(ctx, "/bestblockheader/json", &h)
	return h, err
}

func (a *assetClient) header(ctx context.Context, hash string) (blockHeader, error) {
	var h blockHeader
	err := a.getJSON(ctx, "/header/"+hash+"/json", &h)
	return h, err
}

// coinbase returns the coinbase of a block, from the coinbase_tx member of
// the asset API's block JSON (/block/<hash>/json).
func (a *assetClient) coinbase(ctx context.Context, blockHash string) (*transaction.Transaction, error) {
	var block struct {
		CoinbaseTx *struct {
			TxID string `json:"txid"`
			Hex  string `json:"hex"`
		} `json:"coinbase_tx"`
	}
	if err := a.getJSON(ctx, "/block/"+blockHash+"/json", &block); err != nil {
		return nil, err
	}
	if block.CoinbaseTx == nil {
		return nil, fmt.Errorf("block %s: the asset API returned no coinbase_tx", blockHash)
	}
	cb, err := transaction.NewTransactionFromHex(block.CoinbaseTx.Hex)
	if err != nil {
		return nil, fmt.Errorf("block %s: coinbase_tx: %w", blockHash, err)
	}
	if id := cb.TxID().String(); block.CoinbaseTx.TxID != "" && block.CoinbaseTx.TxID != id {
		return nil, fmt.Errorf("block %s: coinbase_tx hex hashes to %s, not %s", blockHash, id, block.CoinbaseTx.TxID)
	}
	if !cb.IsCoinbase() {
		return nil, fmt.Errorf("block %s: coinbase_tx is not a coinbase", blockHash)
	}
	return cb, nil
}

// utxo is the part of an entry of /utxos/<txid>/json this tool reads.
type utxo struct {
	Vout   uint32 `json:"vout"`
	Status string `json:"status"`
}

func (a *assetClient) utxos(ctx context.Context, txid string) ([]utxo, error) {
	var us []utxo
	err := a.getJSON(ctx, "/utxos/"+txid+"/json", &us)
	return us, err
}

// tx returns a transaction from /tx/<txid>, which serves the Extended
// Format when the node holds it and the raw format otherwise.
func (a *assetClient) tx(ctx context.Context, txid string) (*transaction.Transaction, error) {
	data, err := a.get(ctx, "/tx/"+txid)
	if err != nil {
		return nil, err
	}
	tx, err := transaction.NewTransactionFromBytes(data)
	if err != nil {
		return nil, fmt.Errorf("transaction %s: %w", txid, err)
	}
	if id := tx.TxID().String(); id != txid {
		return nil, fmt.Errorf("asset GET /tx/%s returned transaction %s", txid, id)
	}
	return tx, nil
}

// merkleProof returns the BUMP (BRC-74) of a mined transaction.
func (a *assetClient) merkleProof(ctx context.Context, txid string) (*transaction.MerklePath, error) {
	data, err := a.get(ctx, "/merkle_proof/"+txid)
	if err != nil {
		return nil, err
	}
	mp, err := transaction.NewMerklePathFromBinary(data)
	if err != nil {
		return nil, fmt.Errorf("merkle proof of %s: %w", txid, err)
	}
	return mp, nil
}

// txMeta is the part of /txmeta/<txid>/json this tool reads: the blocks that
// hold the transaction, as parallel arrays.
type txMeta struct {
	BlockHashes  []string `json:"blockHashes"`
	BlockHeights []uint32 `json:"blockHeights"`
}

func (a *assetClient) txMeta(ctx context.Context, txid string) (txMeta, error) {
	var m txMeta
	err := a.getJSON(ctx, "/txmeta/"+txid+"/json", &m)
	return m, err
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("want an http or https URL with a host")
	}
	return u, nil
}

func readBody(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponse {
		return nil, fmt.Errorf("response is larger than %d bytes", maxResponse)
	}
	return data, nil
}

// retryAfter is the wait a 429 asks for, at most maxRetryAfter; one second
// when it names none.
func retryAfter(v string) time.Duration {
	s, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || s < 0 {
		return time.Second
	}
	return min(time.Duration(s)*time.Second, maxRetryAfter)
}

// excerpt is a response body cut to one short line for an error message.
func excerpt(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	if s == "" {
		return "empty body"
	}
	return s
}
