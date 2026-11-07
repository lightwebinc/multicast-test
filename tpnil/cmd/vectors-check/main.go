// Command vectors-check is the reference half of the multicast frame
// conformance corpus: it decodes every vector with the released shard-common
// decoders and compares each decoded field with the corpus, and requires every
// invalid input to be rejected. The independent half, tpnil/vectors/verify.mjs,
// decodes the same bytes from the BRC text alone.
//
//	go run ./tpnil/cmd/vectors-check tpnil/vectors/multicast-frames-v1.json
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/lightwebinc/shard-common/frame"
	"github.com/lightwebinc/shard-common/objfmt"
	"github.com/lightwebinc/shard-common/seqhash"
	"github.com/lightwebinc/shard-common/shard"
)

type corpus struct {
	ID      string `json:"id"`
	Vectors []struct {
		ID       string          `json:"id"`
		Kind     string          `json:"kind"`
		Input    json.RawMessage `json:"input"`
		Expected json.RawMessage `json:"expected"`
	} `json:"vectors"`
	Invalid []struct {
		ID       string `json:"id"`
		Decoder  string `json:"decoder"`
		BytesHex string `json:"bytesHex"`
	} `json:"invalid"`
}

func main() {
	if len(os.Args) != 2 {
		fail("usage: vectors-check CORPUS")
	}
	raw, err := os.ReadFile(os.Args[1])
	check(err)
	var c corpus
	check(json.Unmarshal(raw, &c))
	fmt.Printf("reference decoders (shard-common) on %s\n", c.ID)
	for _, v := range c.Vectors {
		var in, ex map[string]any
		check(json.Unmarshal(v.Input, &in))
		check(json.Unmarshal(v.Expected, &ex))
		switch v.Kind {
		case "tx-frame", "legacy-frame":
			f, err := frame.Decode(unhex(ex["frameHex"]))
			must(v.ID, err == nil, "decode: %v", err)
			must(v.ID, int(f.Version) == num(ex["frameVersion"]), "frame version %d", f.Version)
			must(v.ID, hex.EncodeToString(f.TxID[:]) == in["txid"], "txid")
			must(v.ID, hex.EncodeToString(f.Payload) == in["payloadHex"], "payload")
			if v.Kind == "tx-frame" {
				must(v.ID, fmt.Sprintf("%016x", f.HashKey) == in["hashKey"], "hashKey")
				must(v.ID, strconv.FormatUint(f.SeqNum, 10) == in["seqNum"], "seqNum")
				must(v.ID, hex.EncodeToString(f.SubtreeID[:]) == in["subtreeId"], "subtreeId")
			}
			if want, ok := ex["payloadIsExtendedFormat"].(bool); ok {
				must(v.ID, frame.IsEF(f.Payload) == want, "EF detection")
				other := ex["otherPayloadIsExtendedFormat"].(map[string]any)
				must(v.ID, frame.IsEF(unhex(other["payloadHex"])) == other["isExtendedFormat"].(bool), "EF detection of the raw payload")
			}
		case "group-index":
			txid := arr32(unhex(in["txid"]))
			for _, g := range ex["groups"].([]any) {
				m := g.(map[string]any)
				e := shard.New(0xFF05, 0x000B, uint(num(m["shardBits"])))
				idx := e.GroupIndex(&txid)
				must(v.ID, int(idx) == num(m["groupIndex"]), "group index at %v bits", m["shardBits"])
				must(v.ID, e.Addr(idx, 0).IP.String() == m["asmSiteLocal"], "ASM address")
				must(v.ID, shard.New(0xFF35, 0x000B, uint(num(m["shardBits"]))).Addr(idx, 0).IP.String() == m["ssmSiteLocal"], "SSM address")
			}
		case "hashkey":
			var sender [16]byte
			copy(sender[:], net.ParseIP(in["senderIPv6"].(string)).To16())
			for _, k := range ex["keys"].([]any) {
				m := k.(map[string]any)
				got := seqhash.Hash(sender, uint32(num(in["groupIndex"])), arr32(unhex(m["subtreeId"])))
				must(v.ID, fmt.Sprintf("%016x", got) == m["hashKey"], "hashKey")
			}
		case "fragment-set":
			var whole []byte
			for i, fr := range ex["fragments"].([]any) {
				m := fr.(map[string]any)
				ff, err := frame.DecodeFragment(unhex(m["frameHex"]))
				must(v.ID, err == nil, "fragment %d: %v", i, err)
				must(v.ID, int(ff.FragIndex) == i && int(ff.FragTotal) == num(ex["fragTotal"]), "fragment index or total")
				must(v.ID, int(ff.OrigPayloadLen) == num(ex["origPayloadLength"]), "original length")
				must(v.ID, hex.EncodeToString(ff.TxID[:]) == ex["txid"], "fragment txid")
				whole = append(whole, ff.FragData...)
			}
			d := sha256d(whole)
			must(v.ID, hex.EncodeToString(whole) == in["payloadHex"] && hex.EncodeToString(d[:]) == ex["txid"], "reassembly")
		case "header-frame":
			f, err := frame.DecodeBlockHeader(unhex(ex["frameHex"]))
			must(v.ID, err == nil, "decode: %v", err)
			must(v.ID, hex.EncodeToString(f.TxID[:]) == ex["blockHashInternal"], "block hash")
			must(v.ID, hex.EncodeToString(f.Payload) == in["headerHex"], "header payload")
			must(v.ID, fmt.Sprintf("%016x", f.HashKey) == in["hashKey"] && strconv.FormatUint(f.SeqNum, 10) == in["seqNum"], "flow fields")
		case "beef-object":
			rec := unhex(ex["recordHex"])
			r, n, err := objfmt.DecodeBEEFRecord(rec)
			must(v.ID, err == nil && n == len(rec), "record: %v", err)
			must(v.ID, hex.EncodeToString(r.Object) == in["objectHex"], "record object")
			must(v.ID, fmt.Sprint(r.Topics) == fmt.Sprint(in["topics"]), "record topics %v", r.Topics)
			bf, err := frame.DecodeBEEF(unhex(ex["frameHex"]))
			must(v.ID, err == nil, "frame: %v", err)
			must(v.ID, bytes.Equal(bf.Payload, rec), "frame payload is the record verbatim")
			must(v.ID, hex.EncodeToString(bf.ContentID[:]) == ex["contentId"] && hex.EncodeToString(bf.TopicID[:]) == ex["topicId"], "frame identifiers")
			must(v.ID, int(bf.DeliverCount) == num(in["deliverCount"]), "deliver count")
			tid, obj, _, err := objfmt.DecodeBEEFDelivery(unhex(ex["deliveryHex"]))
			must(v.ID, err == nil && hex.EncodeToString(tid[:]) == ex["topicId"] && bytes.Equal(obj, rec), "delivery record: %v", err)
		case "plane-group":
			tid := objfmt.TopicID(in["topic"].(string))
			must(v.ID, hex.EncodeToString(tid[:]) == ex["topicId"], "topic id")
			for _, g := range ex["groups"].([]any) {
				m := g.(map[string]any)
				p, err := shard.NewPlane(0xFF05, 0x000B, uint(num(m["shardBits"])), uint8(num(in["domain"])))
				must(v.ID, err == nil, "plane: %v", err)
				idx := p.GroupIndex(&tid)
				must(v.ID, int(idx) == num(m["groupIndex"]) && p.Addr(idx, 0).IP.String() == m["asmSiteLocal"], "plane group at %v bits", m["shardBits"])
			}
		default:
			fail("%s: unknown kind %q", v.ID, v.Kind)
		}
		fmt.Printf("reference %s decodes to the corpus fields\n", v.ID)
	}
	for _, b := range c.Invalid {
		var err error
		switch b.Decoder {
		case "tx-frame":
			_, err = frame.Decode(unhex(b.BytesHex))
		case "beef-record":
			// objfmt leaves the object marker to its caller; ingress (the
			// shard-proxy forwarder) applies IsBEEFObject after the grammar,
			// so the reference rule is the two together.
			var r *objfmt.BEEFRecord
			if r, _, err = objfmt.DecodeBEEFRecord(unhex(b.BytesHex)); err == nil && !objfmt.IsBEEFObject(r.Object) {
				err = fmt.Errorf("object marker gate (objfmt.IsBEEFObject, applied at ingress): no BRC-149 version word")
			}
		default:
			fail("%s: unknown decoder %q", b.ID, b.Decoder)
		}
		must(b.ID, err != nil, "accepted an input it must reject")
		fmt.Printf("reference %s rejected: %v\n", b.ID, err)
	}
	fmt.Printf("reference decoders: %d vectors and %d invalid inputs agree with the corpus\n", len(c.Vectors), len(c.Invalid))
}

func sha256d(b []byte) [32]byte {
	f := sha256.Sum256(b)
	return sha256.Sum256(f[:])
}

func unhex(v any) []byte {
	s, _ := v.(string)
	b, err := hex.DecodeString(s)
	check(err)
	return b
}

func arr32(b []byte) [32]byte {
	var a [32]byte
	copy(a[:], b)
	return a
}

func num(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func must(id string, ok bool, format string, args ...any) {
	if !ok {
		fail("%s: %s", id, fmt.Sprintf(format, args...))
	}
}

func check(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "vectors-check: "+format+"\n", args...)
	os.Exit(1)
}
