// Command vectors-gen writes the multicast frame conformance corpus,
// tpnil/vectors/multicast-frames-v1.json, from the released shard-common
// encoders. Header bytes the BRC texts print in their examples are asserted
// byte for byte before anything is written, and every vector records the
// inputs it was built from, so the independent verifier
// (tpnil/vectors/verify.mjs) can rebuild each expected field from the BRC text
// alone.
//
//	go run ./tpnil/cmd/vectors-gen   (from the root of this checkout)
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/lightwebinc/shard-common/frame"
	"github.com/lightwebinc/shard-common/objfmt"
	"github.com/lightwebinc/shard-common/seqhash"
	"github.com/lightwebinc/shard-common/shard"
)

const (
	out        = "tpnil/vectors/multicast-frames-v1.json"
	brcCommit  = "99ee33ea5980313a5c2b01a02a1e42569d981fd6"
	referenced = "github.com/lightwebinc/shard-common@v0.24.0"
)

type vector struct {
	ID       string         `json:"id"`
	BRC      []string       `json:"brc"`
	Clause   string         `json:"clause"`
	Kind     string         `json:"kind"`
	Input    map[string]any `json:"input"`
	Expected map[string]any `json:"expected"`
}

type invalid struct {
	ID       string `json:"id"`
	BRC      string `json:"brc"`
	Clause   string `json:"clause"`
	Decoder  string `json:"decoder"`
	BytesHex string `json:"bytesHex"`
	Reason   string `json:"reason"`
}

func main() {
	var vs []vector
	var bad []invalid

	// BRC-124: the example of "Frame Hex Dump", header bytes as printed.
	txid := mustHex32("11c6900eee6e68d191cd25034a5f872ed29e3b69273906a10e021f39ed866471")
	subtree := mustHex32("baadf498a00ca5a44d1c4d9d103b49017f53cd8cb2a70a9c67fc884ecdd622b5")
	payload200 := deterministic(200)
	copy(payload200[0:5], []byte{0x02, 0x00, 0x00, 0x00, 0x01})
	copy(payload200[196:200], []byte{0, 0, 0, 0})
	f124 := &frame.Frame{TxID: txid, HashKey: 0xA1B2C3D400000001, SeqNum: 1234, SubtreeID: subtree, Payload: payload200}
	b124 := encodeTx(f124)
	spec124 := "e3e1f3e8" + "02bf" + "02" + "00" + hex.EncodeToString(txid[:]) + "a1b2c3d400000001" + "00000000000004d2" + hex.EncodeToString(subtree[:]) + "000000c8"
	assert(hex.EncodeToString(b124[:frame.HeaderSize]) == spec124, "BRC-124 example header differs from shard-common's encoding")
	vs = append(vs, vector{"brc124.frame.example", []string{"BRC-124"}, "Specification, Header Format; Examples, Frame Hex Dump", "tx-frame",
		map[string]any{"txid": h(txid[:]), "hashKey": "a1b2c3d400000001", "seqNum": "1234", "subtreeId": h(subtree[:]), "payloadHex": h(payload200)},
		map[string]any{"frameHex": h(b124), "specHeaderHex": spec124, "frameVersion": 2, "payloadLength": 200}})

	// BRC-124: the legacy BRC-12 frame of "Legacy Frame Hex Dump". shard-common
	// decodes it; no current component emits it, so it is built from the text.
	legacy := append(mustHex("e3e1f3e8"+"02bf"+"01"+"00"+hex.EncodeToString(txid[:])+"000000c8"), payload200...)
	lf, err := frame.Decode(legacy)
	assert(err == nil && lf.Version == frame.FrameVerV1 && lf.TxID == txid && bytes.Equal(lf.Payload, payload200), "legacy frame does not decode")
	vs = append(vs, vector{"brc124.legacy-frame.example", []string{"BRC-124", "BRC-12"}, "Compatibility with BRC-12 Legacy Format; Examples, Legacy Frame Hex Dump", "legacy-frame",
		map[string]any{"txid": h(txid[:]), "payloadHex": h(payload200)},
		map[string]any{"frameHex": h(legacy), "frameVersion": 1, "payloadLength": 200}})

	// BRC-128: an Extended Format payload in an unchanged 0x02 frame.
	var ef struct {
		TxID  string `json:"txid"`
		EFHex string `json:"efHex"`
	}
	readJSON("tpnil/vectors/inputs/ef-transaction.json", &ef)
	efBytes := mustHex(ef.EFHex)
	assert(frame.IsEF(efBytes) && !frame.IsEF(payload200), "EF detection")
	efFrame := encodeTx(&frame.Frame{TxID: reverse32(mustHex32(ef.TxID)), Payload: efBytes})
	vs = append(vs, vector{"brc128.ef-payload", []string{"BRC-128", "BRC-30"}, "Payload Format (BRC-30 Extended Format); Payload Format Detection; Why No Frame Version Bump", "tx-frame",
		map[string]any{"txid": h(reverse(mustHex(ef.TxID))), "hashKey": "0000000000000000", "seqNum": "0", "subtreeId": h(make([]byte, 32)), "payloadHex": ef.EFHex},
		map[string]any{"frameHex": h(efFrame), "frameVersion": 2, "payloadLength": len(efBytes), "payloadIsExtendedFormat": true,
			"otherPayloadIsExtendedFormat": map[string]any{"payloadHex": h(payload200), "isExtendedFormat": false}}})

	// BRC-129: group index and address of the BRC-124 example TxID.
	var groups []map[string]any
	for _, bits := range []uint{1, 2, 8, 12} {
		e := shard.New(0xFF05, 0x000B, bits)
		idx := e.GroupIndex(&txid)
		ssm := shard.New(0xFF35, 0x000B, bits)
		groups = append(groups, map[string]any{"shardBits": bits, "groupIndex": idx,
			"asmSiteLocal": e.Addr(idx, 0).IP.String(), "ssmSiteLocal": ssm.Addr(idx, 0).IP.String()})
	}
	vs = append(vs, vector{"brc129.group-index", []string{"BRC-129"}, "Shard group index derivation; address layout", "group-index",
		map[string]any{"txid": h(txid[:]), "groupId": "000b"}, map[string]any{"groups": groups}})

	// BRC-124: the HashKey formula, for a documentation-prefix sender.
	sender := [16]byte{}
	copy(sender[:], net.ParseIP("2001:db8::1").To16())
	gi := shard.New(0xFF05, 0x000B, 2).GroupIndex(&txid)
	var keys []map[string]any
	for _, st := range [][32]byte{subtree, {}} {
		keys = append(keys, map[string]any{"subtreeId": h(st[:]), "hashKey": fmt.Sprintf("%016x", seqhash.Hash(sender, gi, st))})
	}
	vs = append(vs, vector{"brc124.hashkey", []string{"BRC-124", "BRC-129"}, "Field Definitions, HashKey", "hashkey",
		map[string]any{"senderIPv6": "2001:db8::1", "txid": h(txid[:]), "shardBits": 2, "groupIndex": gi},
		map[string]any{"xxh64Seed": 0, "keys": keys}})

	// BRC-130: the two-fragment example (2000-byte payload, 1348-byte slices).
	p2000 := deterministic(2000)
	fid := sha256d(p2000)
	var frags []map[string]any
	for i, part := range [][]byte{p2000[:1348], p2000[1348:]} {
		buf := make([]byte, frame.HeaderSizeV3+len(part))
		n, err := frame.EncodeFragment(buf, fid, subtree, 0x0102030405060708, uint64(i+1), 2000, uint16(i), 2, 0x00, part)
		assert(err == nil, "fragment encode")
		frags = append(frags, map[string]any{"fragIndex": i, "seqNum": i + 1, "wireBytes": n, "frameHex": h(buf[:n])})
	}
	vs = append(vs, vector{"brc130.two-fragments", []string{"BRC-130"}, "Fragment Header Format; Reassembly; Examples, Two-Fragment Transaction", "fragment-set",
		map[string]any{"payloadHex": h(p2000), "fragDataSize": 1348, "hashKey": "0102030405060708", "subtreeId": h(subtree[:]), "origFrameVer": 0},
		map[string]any{"txid": h(fid[:]), "fragTotal": 2, "origPayloadLength": 2000, "payloadLengths": []int{1348, 652}, "wireBytes": []int{1452, 756}, "fragments": frags}})

	// BRC-135: a header-lane frame for a real 80-byte header.
	var hdr struct {
		BlockHash string `json:"blockHash"`
		HeaderHex string `json:"headerHex"`
	}
	readJSON("tpnil/vectors/inputs/block-header.json", &hdr)
	h80 := mustHex(hdr.HeaderHex)
	bh := sha256d(h80)
	assert(hex.EncodeToString(reverse(bh[:])) == hdr.BlockHash, "block header hash")
	hb := make([]byte, frame.BlockHeaderFrameSize)
	n, err := frame.EncodeBlockHeader(bh, 0x00112233AABBCCDD, 1, h80, hb)
	assert(err == nil, "header frame encode")
	vs = append(vs, vector{"brc135.header-frame", []string{"BRC-135"}, "Frame Header Format; Field Definitions; Examples, BRC-135 Frame Hex Dump", "header-frame",
		map[string]any{"headerHex": hdr.HeaderHex, "hashKey": "00112233aabbccdd", "seqNum": "1"},
		map[string]any{"frameHex": h(hb[:n]), "frameVersion": 7, "blockHashInternal": h(bh[:]), "blockHashDisplay": hdr.BlockHash, "payloadLength": 80}})

	// BRC-148/149: a submission record, its frame, and its delivery record, for
	// a real Atomic BEEF object; then the same object with a label topic.
	var bf struct {
		TxID    string `json:"txid"`
		Topic   string `json:"topic"`
		BEEFHex string `json:"beefHex"`
	}
	readJSON("tpnil/vectors/inputs/atomic-beef.json", &bf)
	obj := mustHex(bf.BEEFHex)
	for _, topics := range [][]string{{bf.Topic}, {bf.Topic, "tm_lab_b"}} {
		rec, err := objfmt.EncodeBEEFRecord(topics, obj)
		assert(err == nil, "record encode")
		tid := objfmt.TopicID(topics[0])
		cid := objfmt.ContentID(rec)
		fb := make([]byte, frame.HeaderSize+len(rec))
		n, err := frame.EncodeBEEF(&frame.BEEFFrame{ContentID: cid, TopicID: tid, DeliverCount: 1, Payload: rec}, fb)
		assert(err == nil, "BEEF frame encode")
		del := objfmt.EncodeBEEFDelivery(tid, rec)
		id := "brc149.record-frame-delivery.one-topic"
		if len(topics) > 1 {
			id = "brc149.record-frame-delivery.with-label"
		}
		vs = append(vs, vector{id, []string{"BRC-148", "BRC-149"}, "BEEF object frame; Submission record; Deliverable topics and labels; Delivery record", "beef-object",
			map[string]any{"topics": topics, "objectHex": bf.BEEFHex, "objectEncoding": "atomic", "deliverCount": 1},
			map[string]any{"recordHex": h(rec), "frameHex": h(fb[:n]), "deliveryHex": h(del), "contentId": h(cid[:]), "topicId": h(tid[:]),
				"frameVersion": 9, "versionWord": "01010101"}})
	}

	// BRC-148: the BEEF plane group of the first topic.
	tid := objfmt.TopicID(bf.Topic)
	var planes []map[string]any
	for _, bits := range []uint{0, 4} {
		p, err := shard.NewPlane(0xFF05, 0x000B, bits, 1)
		assert(err == nil, "plane")
		idx := p.GroupIndex(&tid)
		planes = append(planes, map[string]any{"shardBits": bits, "groupIndex": idx, "asmSiteLocal": p.Addr(idx, 0).IP.String()})
	}
	vs = append(vs, vector{"brc148.plane-group", []string{"BRC-148"}, "Domain-Partitioned Shard Index; The BEEF Object Plane", "plane-group",
		map[string]any{"topic": bf.Topic, "domain": 1, "groupId": "000b"}, map[string]any{"topicId": h(tid[:]), "planeBase": 4096, "groups": planes}})

	// Inputs every decoder MUST reject.
	badMagic := append([]byte(nil), b124...)
	badMagic[0] ^= 0xFF
	bad = append(bad, invalid{"brc124.reject.bad-magic", "BRC-124", "Field Definitions, Network Magic", "tx-frame", h(badMagic), "network magic is not 0xE3E1F3E8"})
	bad = append(bad, invalid{"brc124.reject.truncated-payload", "BRC-124", "Field Definitions, Payload Length", "tx-frame", h(b124[:len(b124)-1]), "fewer payload bytes than Payload Length"})
	rec1, _ := objfmt.EncodeBEEFRecord([]string{bf.Topic}, obj)
	for _, c := range []struct {
		id, reason string
		mut        func([]byte) []byte
	}{
		{"brc149.reject.tag", "record does not lead with the 0xBEEF tag", func(b []byte) []byte { b[0] = 0xBE; b[1] = 0xEE; return b }},
		{"brc149.reject.record-version", "RecordVer is not 0x01", func(b []byte) []byte { b[2] = 0x02; return b }},
		{"brc149.reject.zero-topics", "TopicCount is 0", func(b []byte) []byte { b[3] = 0; return b }},
		{"brc149.reject.empty-topic-name", "a topic NameLen is 0", func(b []byte) []byte {
			return append([]byte{b[0], b[1], b[2], b[3], 0}, b[5+len(bf.Topic):]...)
		}},
		{"brc149.reject.object-marker", "the object leads with no marker of the version-word table", func(b []byte) []byte {
			o := 4 + 1 + len(bf.Topic) + 4
			b[o] ^= 0xFF
			return b
		}},
	} {
		b := c.mut(append([]byte(nil), rec1...))
		bad = append(bad, invalid{c.id, "BRC-149", "Submission record (ingress)", "beef-record", h(b), c.reason})
	}

	corpus := map[string]any{
		"id":             "transport.multicast-frames-v1",
		"name":           "Multicast delivery fabric frames: transaction, fragment, header-lane and BEEF object frames, records and group addressing",
		"brc":            []string{"BRC-124", "BRC-128", "BRC-129", "BRC-130", "BRC-135", "BRC-148", "BRC-149"},
		"version":        "1.0.0",
		"contributor":    "Lightweb Inc.",
		"license":        "Apache-2.0",
		"standardSource": map[string]string{"repository": "https://github.com/bsv-blockchain/BRCs", "commit": brcCommit},
		"reference_impl": referenced,
		"note":           "Expected bytes come from the reference encoders; every field they encode is recorded as an input or an expected value so that an implementation of the BRC text can rebuild it. Header bytes printed in a BRC example are asserted byte for byte.",
		"vectors":        vs,
		"invalid":        bad,
	}
	data, err := json.MarshalIndent(corpus, "", "  ")
	assert(err == nil, "marshal")
	assert(os.WriteFile(out, append(data, '\n'), 0o644) == nil, "write "+out)
	fmt.Printf("wrote %s: %d vectors, %d invalid inputs\n", out, len(vs), len(bad))
}

func encodeTx(f *frame.Frame) []byte {
	buf := make([]byte, frame.HeaderSize+len(f.Payload))
	n, err := frame.Encode(f, buf)
	assert(err == nil, "frame encode")
	return buf[:n]
}

func deterministic(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*7 + 3) % 251)
	}
	return b
}

func sha256d(b []byte) [32]byte {
	first := sha256.Sum256(b)
	return sha256.Sum256(first[:])
}

func reverse(b []byte) []byte {
	r := make([]byte, len(b))
	for i := range b {
		r[i] = b[len(b)-1-i]
	}
	return r
}

func reverse32(b [32]byte) [32]byte {
	var r [32]byte
	copy(r[:], reverse(b[:]))
	return r
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	assert(err == nil, "hex "+s[:min(len(s), 16)])
	return b
}

func mustHex32(s string) [32]byte {
	var a [32]byte
	b := mustHex(s)
	assert(len(b) == 32, "32-byte hex")
	copy(a[:], b)
	return a
}

func h(b []byte) string { return hex.EncodeToString(b) }

func readJSON(path string, v any) {
	raw, err := os.ReadFile(path)
	assert(err == nil, "read "+path)
	assert(json.Unmarshal(raw, v) == nil, "decode "+path)
}

func assert(ok bool, what string) {
	if !ok {
		fmt.Fprintln(os.Stderr, "vectors-gen:", what)
		os.Exit(1)
	}
}
