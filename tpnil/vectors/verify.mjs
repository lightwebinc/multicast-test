#!/usr/bin/env node
// verify.mjs is the independent half of the multicast frame conformance
// corpus. It decodes every vector from the layouts in the BRC text alone, with
// no code shared with the reference implementation and only Node.js built-ins,
// recomputes every derived field (SHA-256, SHA-256d, XXH64, group indices and
// addresses), and requires every invalid input to be rejected.
//
//   node tpnil/vectors/verify.mjs tpnil/vectors/multicast-frames-v1.json
//
// Copyright 2026 Lightweb Inc. Licensed under the Apache License, Version 2.0.

import fs from 'node:fs';
import { createHash } from 'node:crypto';

const MAGIC = 0xe3e1f3e8;
const fromHex = s => Buffer.from(s, 'hex');
const toHex = b => Buffer.from(b).toString('hex');
const sha256 = b => createHash('sha256').update(b).digest();
const sha256d = b => sha256(sha256(b));
const be16 = (b, o) => b.readUInt16BE(o);
const be32 = (b, o) => b.readUInt32BE(o);
const be64 = (b, o) => b.readBigUInt64BE(o);
const hex64 = v => v.toString(16).padStart(16, '0');

class Reject extends Error {}
const need = (ok, why) => { if (!ok) throw new Reject(why); };
const same = (id, got, want, what) => {
  if (String(got) !== String(want)) throw new Error(`${id}: ${what}: got ${got}, want ${want}`);
};

// BRC-124 / BRC-12: a transaction frame, FrameVer 0x02 (92-byte header) or the
// legacy 0x01 (44-byte header).
function txFrame(buf) {
  need(buf.length >= 44, 'shorter than the smallest header');
  need(be32(buf, 0) === MAGIC, 'network magic is not 0xE3E1F3E8');
  const version = buf[6];
  if (version === 0x01) {
    const len = be32(buf, 40);
    need(buf.length === 44 + len, 'payload length does not match the bytes carried');
    return { version, txid: buf.subarray(8, 40), payload: buf.subarray(44) };
  }
  need(version === 0x02, `frame version 0x${version.toString(16)} is not a transaction frame`);
  need(buf.length >= 92, 'shorter than the 92-byte header');
  need(buf[7] === 0x00, 'reserved byte is not zero');
  const len = be32(buf, 88);
  need(buf.length === 92 + len, 'payload length does not match the bytes carried');
  return {
    version, txid: buf.subarray(8, 40), hashKey: be64(buf, 40), seqNum: be64(buf, 48),
    subtreeId: buf.subarray(56, 88), payload: buf.subarray(92),
  };
}

// BRC-128, Payload Format Detection: the BRC-30 marker after the version word.
const isExtendedFormat = p => p.length >= 10 && toHex(p.subarray(4, 10)) === '0000000000ef';

// BRC-129: groupIndex = BE32(txid[0:4]) >> (32 - shardBits); BRC-148 states the
// zero-width plane (shardBits 0) explicitly: one group, index 0. A plain
// `>>> 32` in JavaScript is a shift by 0, which the text rules out.
function shardIndex(key, bits) {
  if (bits === 0) return 0;
  const mask = bits >= 32 ? 0xffffffff : (1 << bits) - 1;
  return ((be32(key, 0) >>> (32 - bits)) & mask) >>> 0;
}

// RFC 5952 text form of FF0X::B:IDX (the address layout of BRC-129).
function groupAddress(prefix, groupId, index) {
  const g = [prefix, 0, 0, 0, 0, 0, groupId, index & 0xffff];
  let best = -1, bestLen = 0;
  for (let i = 0; i < 8;) {
    if (g[i] !== 0) { i++; continue; }
    let j = i;
    while (j < 8 && g[j] === 0) j++;
    if (j - i > bestLen && j - i >= 2) { best = i; bestLen = j - i; }
    i = j;
  }
  const part = a => a.map(x => x.toString(16)).join(':');
  if (best < 0) return part(g);
  return `${part(g.slice(0, best))}::${part(g.slice(best + bestLen))}`;
}

// XXH64, seed 0: the HashKey function of BRC-124.
const M64 = (1n << 64n) - 1n;
const P1 = 0x9e3779b185ebca87n, P2 = 0xc2b2ae3d27d4eb4fn, P3 = 0x165667b19e3779f9n;
const P4 = 0x85ebca77c2b2ae63n, P5 = 0x27d4eb2f165667c5n;
const rotl = (x, r) => ((x << r) | (x >> (64n - r))) & M64;
const round = (acc, lane) => (rotl((acc + lane * P2) & M64, 31n) * P1) & M64;
const merge = (acc, v) => (((acc ^ round(0n, v)) * P1) + P4) & M64;
function xxh64(buf, seed = 0n) {
  let p = 0, h;
  const n = buf.length;
  if (n >= 32) {
    let v1 = (seed + P1 + P2) & M64, v2 = (seed + P2) & M64, v3 = seed, v4 = (seed - P1) & M64;
    while (p <= n - 32) {
      v1 = round(v1, buf.readBigUInt64LE(p)); v2 = round(v2, buf.readBigUInt64LE(p + 8));
      v3 = round(v3, buf.readBigUInt64LE(p + 16)); v4 = round(v4, buf.readBigUInt64LE(p + 24));
      p += 32;
    }
    h = (rotl(v1, 1n) + rotl(v2, 7n) + rotl(v3, 12n) + rotl(v4, 18n)) & M64;
    h = merge(merge(merge(merge(h, v1), v2), v3), v4);
  } else {
    h = (seed + P5) & M64;
  }
  h = (h + BigInt(n)) & M64;
  for (; p + 8 <= n; p += 8) h = ((rotl(h ^ round(0n, buf.readBigUInt64LE(p)), 27n) * P1) + P4) & M64;
  if (p + 4 <= n) { h = ((rotl(h ^ ((BigInt(buf.readUInt32LE(p)) * P1) & M64), 23n) * P2) + P3) & M64; p += 4; }
  for (; p < n; p++) h = (rotl(h ^ ((BigInt(buf[p]) * P5) & M64), 11n) * P1) & M64;
  h ^= h >> 33n; h = (h * P2) & M64; h ^= h >> 29n; h = (h * P3) & M64; h ^= h >> 32n;
  return h;
}

function ipv6Bytes(text) {
  const [head, tail] = text.includes('::') ? text.split('::') : [text, null];
  const a = head ? head.split(':') : [], b = tail ? tail.split(':') : [];
  const groups = tail === null ? a : [...a, ...Array(8 - a.length - b.length).fill('0'), ...b];
  const out = Buffer.alloc(16);
  groups.forEach((g, i) => out.writeUInt16BE(parseInt(g, 16), 2 * i));
  return out;
}

// BRC-130: one fragment, a 104-byte header and its slice.
function fragment(buf) {
  need(buf.length >= 104 && be32(buf, 0) === MAGIC && buf[6] === 0x03, 'not a BRC-130 fragment');
  need(toHex(buf.subarray(101, 104)) === '000000', 'reserved bytes are not zero');
  const len = be32(buf, 88);
  need(buf.length === 104 + len, 'fragment length does not match the bytes carried');
  return {
    txid: buf.subarray(8, 40), hashKey: be64(buf, 40), seqNum: be64(buf, 48), subtreeId: buf.subarray(56, 88),
    origLength: be32(buf, 92), index: be16(buf, 96), total: be16(buf, 98), origFrameVer: buf[100], data: buf.subarray(104),
  };
}

// BRC-135: the header-lane frame.
function headerFrame(buf) {
  need(buf.length === 172 && be32(buf, 0) === MAGIC && buf[6] === 0x07 && buf[7] === 0, 'not a BRC-135 frame');
  need(buf.subarray(56, 88).every(x => x === 0), 'LayoutPad32 is not zero');
  need(be32(buf, 88) === 80, 'payload length is not 80');
  const payload = buf.subarray(92);
  need(sha256d(payload).equals(buf.subarray(8, 40)), 'BlockHash is not SHA-256d of the header');
  return { blockHash: buf.subarray(8, 40), hashKey: be64(buf, 40), seqNum: be64(buf, 48), header: payload };
}

// BRC-149: submission record, BEEF object frame and delivery record.
const MARKERS = new Set(['0100beef', '0200beef', '01010101']);
function record(buf) {
  need(buf.length >= 9 && be16(buf, 0) === 0xbeef, 'record does not lead with the 0xBEEF tag');
  need(buf[2] === 0x01, 'RecordVer is not 0x01');
  const count = buf[3];
  need(count >= 1 && count <= 15, 'TopicCount outside 1..15');
  let o = 4;
  const topics = [];
  const utf8 = new TextDecoder('utf-8', { fatal: true });
  for (let i = 0; i < count; i++) {
    need(o < buf.length, 'record ends inside its topic list');
    const n = buf[o];
    need(n >= 1 && n <= 64, 'a topic NameLen outside 1..64');
    need(o + 1 + n <= buf.length, 'record ends inside a topic name');
    let name;
    try { name = utf8.decode(buf.subarray(o + 1, o + 1 + n)); } catch { throw new Reject('a topic name is not UTF-8'); }
    topics.push(name);
    o += 1 + n;
  }
  need(o + 4 <= buf.length, 'record ends before ObjectLen');
  const len = be32(buf, o);
  need(len >= 1 && buf.length === o + 4 + len, 'ObjectLen does not match the object carried');
  const object = buf.subarray(o + 4);
  need(MARKERS.has(toHex(object.subarray(0, 4))), 'the object leads with no marker of the version-word table');
  return { topics, object };
}
function beefFrame(buf) {
  need(buf.length >= 92 && be32(buf, 0) === MAGIC && buf[6] === 0x09, 'not a BRC-149 frame');
  const len = be32(buf, 88);
  need(buf.length === 92 + len, 'payload length does not match the bytes carried');
  const payload = buf.subarray(92);
  need(sha256d(payload).equals(buf.subarray(8, 40)), 'ContentID is not SHA-256d of the payload');
  return { deliverCount: buf[7], contentId: buf.subarray(8, 40), hashKey: be64(buf, 40), seqNum: be64(buf, 48), topicId: buf.subarray(56, 88), payload };
}
function delivery(buf) {
  need(buf.length >= 37, 'delivery record too short');
  const len = be32(buf, 32);
  need(len >= 1 && buf.length === 36 + len, 'PayloadLen does not match the payload carried');
  return { topicId: buf.subarray(0, 32), payload: buf.subarray(36) };
}

const path = process.argv[2];
if (!path) { console.error('usage: verify.mjs CORPUS'); process.exit(2); }
const corpus = JSON.parse(fs.readFileSync(path, 'utf8'));
console.log(`independent verifier (BRC text only) on ${corpus.id}`);

for (const v of corpus.vectors) {
  const { input: i, expected: e } = v;
  switch (v.kind) {
    case 'tx-frame':
    case 'legacy-frame': {
      const f = txFrame(fromHex(e.frameHex));
      same(v.id, f.version, e.frameVersion, 'frame version');
      same(v.id, toHex(f.txid), i.txid, 'txid');
      same(v.id, toHex(f.payload), i.payloadHex, 'payload');
      same(v.id, f.payload.length, e.payloadLength, 'payload length');
      if (v.kind === 'tx-frame') {
        same(v.id, hex64(f.hashKey), i.hashKey, 'HashKey');
        same(v.id, f.seqNum, i.seqNum, 'SeqNum');
        same(v.id, toHex(f.subtreeId), i.subtreeId, 'Subtree ID');
      }
      if (e.specHeaderHex) same(v.id, e.frameHex.slice(0, 184), e.specHeaderHex, 'header against the BRC example');
      if ('payloadIsExtendedFormat' in e) {
        same(v.id, isExtendedFormat(f.payload), e.payloadIsExtendedFormat, 'EF detection');
        same(v.id, isExtendedFormat(fromHex(e.otherPayloadIsExtendedFormat.payloadHex)), e.otherPayloadIsExtendedFormat.isExtendedFormat, 'EF detection of the raw payload');
      }
      break;
    }
    case 'group-index':
      for (const g of e.groups) {
        const idx = shardIndex(fromHex(i.txid), g.shardBits);
        same(v.id, idx, g.groupIndex, `group index at ${g.shardBits} bits`);
        same(v.id, groupAddress(0xff05, parseInt(i.groupId, 16), idx), g.asmSiteLocal, 'ASM site-local address');
        same(v.id, groupAddress(0xff35, parseInt(i.groupId, 16), idx), g.ssmSiteLocal, 'SSM site-local address');
      }
      break;
    case 'hashkey': {
      same(v.id, shardIndex(fromHex(i.txid), i.shardBits), i.groupIndex, 'group index');
      for (const k of e.keys) {
        const gi = Buffer.alloc(4); gi.writeUInt32BE(i.groupIndex);
        const key = xxh64(Buffer.concat([ipv6Bytes(i.senderIPv6), gi, fromHex(k.subtreeId)]));
        same(v.id, hex64(key), k.hashKey, 'XXH64(senderIPv6 ∥ groupIdx ∥ subtreeID)');
      }
      break;
    }
    case 'fragment-set': {
      const frags = e.fragments.map(f => fragment(fromHex(f.frameHex)));
      frags.forEach((f, n) => {
        same(v.id, f.index, n, 'FragIndex'); same(v.id, f.total, e.fragTotal, 'FragTotal');
        same(v.id, f.origLength, e.origPayloadLength, 'OrigPayloadLen'); same(v.id, f.data.length, e.payloadLengths[n], 'PayloadLen');
        same(v.id, 104 + f.data.length, e.wireBytes[n], 'bytes on the wire'); same(v.id, f.origFrameVer, i.origFrameVer, 'OrigFrameVer');
        same(v.id, hex64(f.hashKey), i.hashKey, 'HashKey'); same(v.id, toHex(f.subtreeId), i.subtreeId, 'Subtree ID');
        same(v.id, toHex(f.txid), e.txid, 'fragment TxID');
      });
      const whole = Buffer.concat(frags.map(f => f.data));
      same(v.id, toHex(whole), i.payloadHex, 'reassembled payload');
      same(v.id, toHex(sha256d(whole)), e.txid, 'TxID = SHA256d(reassembled payload)');
      same(v.id, frags[0].data.length, i.fragDataSize, 'fragment data size');
      break;
    }
    case 'header-frame': {
      const f = headerFrame(fromHex(e.frameHex));
      same(v.id, toHex(f.header), i.headerHex, 'header payload');
      same(v.id, toHex(f.blockHash), e.blockHashInternal, 'BlockHash');
      same(v.id, toHex(Buffer.from(f.blockHash).reverse()), e.blockHashDisplay, 'BlockHash in display order');
      same(v.id, hex64(f.hashKey), i.hashKey, 'HashKey'); same(v.id, f.seqNum, i.seqNum, 'SeqNum');
      break;
    }
    case 'beef-object': {
      const r = record(fromHex(e.recordHex));
      same(v.id, JSON.stringify(r.topics), JSON.stringify(i.topics), 'topics');
      same(v.id, toHex(r.object), i.objectHex, 'object');
      same(v.id, toHex(r.object.subarray(0, 4)), e.versionWord, 'version word');
      const f = beefFrame(fromHex(e.frameHex));
      same(v.id, toHex(f.payload), e.recordHex, 'frame payload is the record verbatim');
      same(v.id, toHex(f.contentId), e.contentId, 'ContentID');
      same(v.id, toHex(f.topicId), toHex(sha256(Buffer.from(i.topics[0], 'utf8'))), 'TopicID = SHA-256(first topic)');
      same(v.id, toHex(f.topicId), e.topicId, 'TopicID');
      same(v.id, f.deliverCount, i.deliverCount, 'DeliverCount');
      const d = delivery(fromHex(e.deliveryHex));
      same(v.id, toHex(d.topicId), e.topicId, 'delivery TopicID');
      same(v.id, toHex(d.payload), e.recordHex, 'delivery payload is the frame payload verbatim');
      break;
    }
    case 'plane-group': {
      const tid = sha256(Buffer.from(i.topic, 'utf8'));
      same(v.id, toHex(tid), e.topicId, 'TopicID');
      same(v.id, i.domain << 12, e.planeBase, 'planeBase(domain) = domain << 12');
      for (const g of e.groups) {
        const idx = e.planeBase + shardIndex(tid, g.shardBits);
        same(v.id, idx, g.groupIndex, `plane group at ${g.shardBits} bits`);
        same(v.id, groupAddress(0xff05, parseInt(i.groupId, 16), idx), g.asmSiteLocal, 'ASM site-local address');
      }
      break;
    }
    default:
      throw new Error(`${v.id}: unknown kind ${v.kind}`);
  }
  console.log(`independent ${v.id} matches the BRC text`);
}

for (const b of corpus.invalid) {
  const decode = { 'tx-frame': txFrame, 'beef-record': record }[b.decoder];
  if (!decode) throw new Error(`${b.id}: unknown decoder ${b.decoder}`);
  try {
    decode(fromHex(b.bytesHex));
  } catch (err) {
    if (!(err instanceof Reject)) throw err;
    console.log(`independent ${b.id} rejected: ${err.message}`);
    continue;
  }
  throw new Error(`${b.id}: accepted an input the BRC text rejects`);
}
console.log(`independent verifier: ${corpus.vectors.length} vectors and ${corpus.invalid.length} invalid inputs agree with the BRC text`);
