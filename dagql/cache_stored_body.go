package dagql

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
)

// A blob-backed cache, the Cloud, never decodes a stored value: it indexes,
// chooses, sends and saves records. So it holds each stored record's envelope
// in transfer form as compressed JSON (storedBody), beside the facts its
// readers need without it: the entries its references name, which a bundle
// checks, and the parts it declares absent or pending, which part answers and
// borrowing read. Its persistedEnvelope keeps only the envelope's small
// fields (lightStoredEnvelope), which tell a stored value by its presence and
// its kind.
//
// The body holds the record's call frame too, compressed, once the body
// worker has made it, and the entry then holds no decoded frame
// (entryFrame). The cache reads a stored frame for three things: its recipe
// digest, which the body keeps beside it, for the recipes of held rows and of
// what references the entry; whether the entry has one; and the frame as
// JSON, which a bundle carries as it is held, a save writes out and a
// measure counts. Only a prune's log of a removed entry, and the rare reader
// that wants the frame itself, decode it, and keep nothing.
//
// A bundle carries the compressed envelope as it is, and the message's
// encoding writes it out (TransferredValue.MarshalJSON), so a build neither
// decodes nor encodes a record. A save writes the body as it is
// (encodePersistedResultRow), and a restore takes it back without decoding
// it.
//
// A merge installs a record whole, as on an engine. The cache's body worker
// compresses it after the merge, outside every lock (storedBodyWorker), so a
// merge's commit costs what it did. Until then the entry holds its whole
// envelope, and its readers take either (storedRecordLocked,
// declaredPartsLocked).

// storedBodyLevel is the zstd level stored envelopes are compressed at: the
// default level, at which bodies of the service's saved stores measured 0.9
// to 1.5 KB a record against 2.3 to 9.9 KB encoded, 36 to 40 µs a record to
// compress, once, and 5 to 9 µs to decompress. Compressing runs of records
// together compresses better, but a reader would then decompress the run.
const storedBodyLevel = zstd.SpeedDefault

var (
	storedBodyCodecOnce sync.Once
	storedBodyEncoder   *zstd.Encoder
	storedBodyDecoder   *zstd.Decoder
	storedBodyCodecErr  error
)

// storedBodyCodec returns the encoder and decoder of stored bodies, shared:
// EncodeAll and DecodeAll may be called concurrently. The encoder compresses
// one record at a time, since a cache's one body worker compresses, and a
// save only what the worker hasn't yet; each concurrent encoder would hold
// tables of its own.
func storedBodyCodec() (*zstd.Encoder, *zstd.Decoder, error) {
	storedBodyCodecOnce.Do(func() {
		storedBodyEncoder, storedBodyCodecErr = zstd.NewWriter(nil, zstd.WithEncoderLevel(storedBodyLevel), zstd.WithEncoderConcurrency(1))
		if storedBodyCodecErr == nil {
			storedBodyDecoder, storedBodyCodecErr = zstd.NewReader(nil)
		}
	})
	return storedBodyEncoder, storedBodyDecoder, storedBodyCodecErr
}

// storedBody is a blob-backed cache's stored record held compressed.
type storedBody struct {
	// envelope is the record's envelope in transfer form, without offers,
	// as JSON compressed with zstd, and envelopeBytes the JSON's length.
	envelope      []byte
	envelopeBytes int64
	// frame is, once the body holds it, the record's call frame as JSON
	// compressed with zstd, frameBytes the JSON's length, and recipe the
	// frame's recipe digest. The entry then holds no decoded frame: the body
	// is its frame (entryFrame). The JSON is the frame's transfer form, as a
	// bundle carries it, and the frame itself, as a save writes it: a frame
	// whose two forms differ stays decoded (withFrame).
	frame      []byte
	frameBytes int64
	recipe     digest.Digest
	// call is, while the body holds no frame, the decoded frame the body was
	// made with. refs and parts are of the record and its frame: the entries
	// the record's child and call references name, sorted, and the parts it
	// declares absent or pending. An entry's decoded call can be replaced
	// after (storedFactsLocked); a frame the body holds is replaced only with
	// the body.
	call  *ResultCall
	refs  []uint64
	parts []declaredPart
	// size is the entry's value's encoded size in a bundle, once StoredSizes
	// has measured it, with what else it read.
	size atomic.Pointer[storedValueSize]
}

// declaredPart is one part a stored record declares absent or pending: its
// address and that address's key, its state and the kind of its value, which
// part answers, borrowing and the bundle's check of its own outputs read.
type declaredPart struct {
	key     string
	address PersistedPartAddress
	state   string
	kind    string
}

// output is the part as an output of the record's mapping, with what a check
// of an output against it reads (checkOutputPart).
func (p declaredPart) output() CapturedCodecOutput {
	return CapturedCodecOutput{Address: p.address, State: p.state, ValueKind: p.kind}
}

// newStoredBody compresses res's stored record of env and call, normalized
// for transfer, and works out its facts. It reads only env and call, which
// the cache replaces and never changes in place, so it needs no lock.
func newStoredBody(res *sharedResult, env *PersistedResultEnvelope, call *ResultCall) (*storedBody, error) {
	kept, err := normalizeStoredTransfer(res, env, call)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(kept.record.Envelope)
	if err != nil {
		return nil, err
	}
	return compressStoredBody(raw, call, kept.refs, kept.parts)
}

// compressStoredBody is a body of the envelope JSON raw and its facts.
func compressStoredBody(raw []byte, call *ResultCall, refs []uint64, parts []declaredPart) (*storedBody, error) {
	enc, _, err := storedBodyCodec()
	if err != nil {
		return nil, err
	}
	// EncodeAll's own buffer has the input's capacity: the body keeps a copy
	// of what it used.
	return &storedBody{envelope: bytes.Clone(enc.EncodeAll(raw, nil)), envelopeBytes: int64(len(raw)), call: call, refs: refs, parts: parts}, nil
}

// withFrame returns a body of b's record with call, the entry's decoded
// frame, held in it too, compressed, with its recipe digest, or b itself if
// the frame can't be held: if its transfer form, which a bundle carries,
// differs from the frame, which a save writes, or it can't be encoded or
// its recipe derived. Deriving the recipe can read the cache, so the caller
// holds no lock. It returns an error only if the codec can't be made.
func (b *storedBody) withFrame(c *Cache, call *ResultCall) (*storedBody, error) {
	enc, _, err := storedBodyCodec()
	if err != nil {
		return nil, err
	}
	return b.withFrameEncoded(c, call, enc)
}

// withFrameEncoded is withFrame, compressing with enc.
func (b *storedBody) withFrameEncoded(c *Cache, call *ResultCall, enc *zstd.Encoder) (*storedBody, error) {
	if b.frame != nil || call == nil || b.call != call {
		return b, nil
	}
	// A call a bundle's check would refuse stays decoded, for the check to
	// refuse it as it would have (validateStoredRecordShape).
	if call.Type == nil || call.Type.NamedType == "Query" {
		return b, nil
	}
	raw, ok := storedFrameJSON(call)
	if !ok {
		return b, nil
	}
	// A frame whose recipe can't be derived stays decoded too: its readers
	// derive it, and report the error, as they would have.
	recipe, recipeErr := call.deriveRecipeDigest(c)
	if recipeErr != nil {
		return b, nil //nolint:nilerr // the frame stays decoded; its readers report the error
	}
	return &storedBody{
		envelope:      b.envelope,
		envelopeBytes: b.envelopeBytes,
		frame:         bytes.Clone(enc.EncodeAll(raw, nil)),
		frameBytes:    int64(len(raw)),
		recipe:        recipe,
		refs:          b.refs,
		parts:         b.parts,
	}, nil
}

// storedFrameJSON returns call's JSON, if it is the JSON of its transfer
// form too, which it is unless call carries an extra digest the transfer
// form leaves out.
func storedFrameJSON(call *ResultCall) ([]byte, bool) {
	raw, err := json.Marshal(call)
	if err != nil {
		return nil, false
	}
	if transferFormIsTheFrame(call) {
		return raw, true
	}
	transfer, err := normalizeTransferCall(call)
	if err != nil {
		return nil, false
	}
	sent, err := json.Marshal(transfer)
	if err != nil || !bytes.Equal(raw, sent) {
		return nil, false
	}
	return raw, true
}

// transferFormIsTheFrame reports whether normalizeTransferCall leaves call
// as it is: whether no frame it walks has an extra digest without a digest,
// which the transfer form drops. A call frame's references name entries by
// number, which normalizing keeps: visitResultCallReferences reports no
// recipe ID for them, so the extra digests are all it can change. It only
// reads call, which other readers share.
func transferFormIsTheFrame(call *ResultCall) bool {
	same := true
	if err := walkTransferCalls(call, func(frame *ResultCall) error {
		for _, extra := range frame.ExtraDigests {
			if extra.Digest == "" {
				same = false
			}
		}
		return nil
	}, nil); err != nil {
		return false
	}
	return same
}

// reframe returns a body of b's record with frame, the decoded frame that
// replaces the one b holds, its facts worked out again, and the frame held in
// it if it can be (withFrame). Deriving reads the cache, so the caller holds
// no lock.
func (b *storedBody) reframe(c *Cache, id sharedResultID, frame *ResultCall) (*storedBody, error) {
	rec, err := b.record(uint64(id))
	if err != nil {
		return nil, err
	}
	kept, err := normalizeStoredTransfer(&sharedResult{id: id}, &rec.Envelope, frame)
	if err != nil {
		return nil, err
	}
	next := &storedBody{envelope: b.envelope, envelopeBytes: b.envelopeBytes, call: frame, refs: kept.refs, parts: kept.parts}
	return next.withFrame(c, frame)
}

// holdsFrame reports whether there is a body and it holds the frame.
func (b *storedBody) holdsFrame() bool {
	return b != nil && b.frame != nil
}

// frameJSON returns the JSON of the frame the body holds, decompressed.
func (b *storedBody) frameJSON() ([]byte, error) {
	_, dec, err := storedBodyCodec()
	if err != nil {
		return nil, err
	}
	raw, err := dec.DecodeAll(b.frame, make([]byte, 0, b.frameBytes))
	if err != nil {
		return nil, fmt.Errorf("decompress stored call frame: %w", err)
	}
	return raw, nil
}

// decodeFrame returns the frame the body holds, decoded: a copy of its own,
// which nothing else holds.
func (b *storedBody) decodeFrame() (*ResultCall, error) {
	raw, err := b.frameJSON()
	if err != nil {
		return nil, err
	}
	frame := &ResultCall{}
	if err := json.Unmarshal(raw, frame); err != nil {
		return nil, fmt.Errorf("decode stored call frame: %w", err)
	}
	return frame, nil
}

// entryFrame is an entry's call frame as read under egraphMu: decoded, or
// held compressed in the entry's stored body. Both are replaced, never
// changed in place, so it can be read after the lock.
type entryFrame struct {
	call *ResultCall
	body *storedBody
}

// entryFrameLocked returns res's call frame. Requires egraphMu, or an entry
// no other goroutine changes.
func (res *sharedResult) entryFrameLocked() entryFrame {
	if call := res.loadResultCall(); call != nil {
		return entryFrame{call: call}
	}
	if body := res.stored; body != nil && body.frame != nil {
		return entryFrame{body: body}
	}
	return entryFrame{}
}

// hasCallFrameLocked reports whether res has a call frame, decoded or held
// compressed. Requires egraphMu.
func (res *sharedResult) hasCallFrameLocked() bool {
	return res.entryFrameLocked().ok()
}

// ok reports whether there is a frame.
func (f entryFrame) ok() bool {
	return f.call != nil || f.body != nil
}

// recipeDigest returns the frame's recipe digest: derived from the decoded
// frame, which memoizes it, or the one the body keeps beside its frame.
// Deriving can read the cache, so the caller holds no lock.
func (f entryFrame) recipeDigest(c *Cache) (digest.Digest, error) {
	if f.call != nil {
		return f.call.deriveRecipeDigest(c)
	}
	if f.body != nil {
		return f.body.recipe, nil
	}
	return "", errors.New("missing result call frame")
}

// decode returns the frame decoded: the entry's own, or a copy decoded from
// its body, which nothing else holds.
func (f entryFrame) decode() (*ResultCall, error) {
	if f.call != nil {
		return f.call, nil
	}
	if f.body != nil {
		return f.body.decodeFrame()
	}
	return nil, nil
}

// envelopeJSON returns the body's envelope JSON, decompressed.
func (b *storedBody) envelopeJSON() ([]byte, error) {
	_, dec, err := storedBodyCodec()
	if err != nil {
		return nil, err
	}
	raw, err := dec.DecodeAll(b.envelope, make([]byte, 0, b.envelopeBytes))
	if err != nil {
		return nil, fmt.Errorf("decompress stored envelope: %w", err)
	}
	return raw, nil
}

// record returns the body's record of the entry with the number, decoded:
// its envelope in transfer form and its call.
func (b *storedBody) record(number uint64) (PersistedRecord, error) {
	raw, err := b.envelopeJSON()
	if err != nil {
		return PersistedRecord{}, err
	}
	rec := PersistedRecord{ResultID: number, Call: b.call}
	if err := json.Unmarshal(raw, &rec.Envelope); err != nil {
		return PersistedRecord{}, fmt.Errorf("decode stored envelope: %w", err)
	}
	return rec, nil
}

// declaredPartsOf returns the parts of outputs declared absent or pending.
func declaredPartsOf(outputs []CapturedCodecOutput) ([]declaredPart, error) {
	var parts []declaredPart
	for _, output := range outputs {
		if output.State != "absent" && output.State != "pending" {
			continue
		}
		key, err := partAddressKey(output.Address)
		if err != nil {
			return nil, err
		}
		kind := output.ValueKind
		if output.Value != nil {
			kind = output.Value.Kind
		}
		parts = append(parts, declaredPart{key: key, address: output.Address, state: output.State, kind: kind})
	}
	return parts, nil
}

// lightStoredEnvelope is env without its value: the fields that tell a stored
// value by its presence and its kind, and its offers.
func lightStoredEnvelope(env *PersistedResultEnvelope) *PersistedResultEnvelope {
	return &PersistedResultEnvelope{
		Imported:              env.Imported,
		PendingOffers:         env.PendingOffers,
		PersistDeclined:       env.PersistDeclined,
		Version:               env.Version,
		Kind:                  env.Kind,
		TypeName:              env.TypeName,
		ObjectCodec:           env.ObjectCodec,
		ResultID:              env.ResultID,
		SessionResourceHandle: env.SessionResourceHandle,
	}
}

// setStoredBodyLocked makes body res's stored record: res keeps the light
// envelope of env, and no kept transfer record. A body that holds the frame
// is the entry's frame: res keeps no decoded one. Requires egraphMu for
// writing, or an entry no reader can reach yet.
func (c *Cache) setStoredBodyLocked(res *sharedResult, env *PersistedResultEnvelope, body *storedBody) {
	// A size measured for the same record and call is the body's: a value is
	// measured the same whole or compressed, and its frame the same decoded
	// or held in the body.
	made := body.call
	if body.frame != nil {
		made = res.loadResultCall()
	}
	if kept := res.storedTransfer.Load(); kept != nil && kept.envelope == env && kept.call == made {
		if size := kept.size.Load(); size != nil {
			carried := *size
			carried.call = body.call
			body.size.Store(&carried)
		}
	}
	res.payloadMu.Lock()
	res.persistedEnvelope = lightStoredEnvelope(env)
	res.stored = body
	res.payloadMu.Unlock()
	if body.frame != nil {
		// The frame is not replaced, only held otherwise: no new frame
		// generation (dropResultCall).
		res.dropResultCall()
	}
	res.storedTransfer.Store(nil)
	if res.id != 0 && c.resultsByID[res.id] == res {
		c.setResultPayloadBytesLocked(res, int64(len(body.envelope)))
	} else {
		res.payloadBytes = int64(len(body.envelope))
	}
}

// storedRecordLocked returns res's stored record in transfer form, without
// offers: decoded from its body, or normalized from its whole envelope. The
// result is shared: callers copy what they change. Requires egraphMu.
func (c *Cache) storedRecordLocked(res *sharedResult) (PersistedRecord, error) {
	if body := res.stored; body != nil {
		rec, err := body.record(uint64(res.id))
		if err != nil {
			return rec, err
		}
		rec.Call, err = res.entryFrameLocked().decode()
		return rec, err
	}
	return c.storedTransferRecordLocked(res)
}

// wholeEnvelope returns the payload's whole stored envelope: decoded from its
// body, with the light envelope's offers and marks, when it is held
// compressed, or the one it holds.
func (s sharedResultPayloadState) wholeEnvelope(number uint64) (*PersistedResultEnvelope, error) {
	if s.stored == nil {
		return s.persistedEnvelope, nil
	}
	rec, err := s.stored.record(number)
	if err != nil {
		return nil, err
	}
	rec.Envelope.PendingOffers = s.persistedEnvelope.PendingOffers
	rec.Envelope.PersistDeclined = s.persistedEnvelope.PersistDeclined
	return &rec.Envelope, nil
}

// storedFactsLocked returns the facts of res's compressed record: the
// entries its references name and the parts it declares. They are its
// body's, unless its decoded call was replaced since the body was made, as
// teaching it a digest does: then they are worked out again from the body
// and the call. A body that holds the frame is replaced with it, so its
// facts are the frame's. Requires egraphMu.
func (res *sharedResult) storedFactsLocked() ([]uint64, []declaredPart, error) {
	body := res.stored
	call := res.loadResultCall()
	if body.frame != nil || body.call == call {
		return body.refs, body.parts, nil
	}
	rec, err := body.record(uint64(res.id))
	if err != nil {
		return nil, nil, err
	}
	kept, err := normalizeStoredTransfer(&sharedResult{id: res.id}, &rec.Envelope, call)
	if err != nil {
		return nil, nil, err
	}
	return kept.refs, kept.parts, nil
}

// declaredPartsLocked returns the parts res's stored record declares absent
// or pending: its body's, or mapped from its whole envelope. Requires
// egraphMu.
func (res *sharedResult) declaredPartsLocked() ([]declaredPart, error) {
	if res.stored != nil {
		_, parts, err := res.storedFactsLocked()
		return parts, err
	}
	if res.persistedEnvelope == nil {
		return nil, nil
	}
	outputs, err := mapTransferredOutputs(PersistedRecord{ResultID: uint64(res.id), Envelope: *res.persistedEnvelope, Call: res.loadResultCall()})
	if err != nil {
		return nil, err
	}
	return declaredPartsOf(outputs)
}

// The saved row of a blob-backed cache's stored record: its self payload is
// a header and the body as it is held, so that a save writes the body and a
// restore takes it back, neither decoding it.
//
//	storedRowMark | uvarint length of the header | header JSON (storedRowHeader) | body
//
// Another row's self payload is its envelope's JSON, which never starts with
// the mark. A blob-backed cache saves in a format of its own
// (persistenceSchemaVersion), so a store saved before rows took this form is
// wiped once.

// storedRowMark starts a stored record's saved self payload.
const storedRowMark = 0

// isStoredRow reports whether a saved self payload is a stored record's.
func isStoredRow(payload []byte) bool {
	return len(payload) > 0 && payload[0] == storedRowMark
}

// storedRowHeader is a saved stored record's header: its light envelope, with
// the parts the cache stores and its decline mark, as an engine's saved
// envelope carries them, and the body's facts.
type storedRowHeader struct {
	Envelope      PersistedResultEnvelope `json:"envelope"`
	EnvelopeBytes int64                   `json:"envelopeBytes"`
	Refs          []uint64                `json:"refs,omitempty"`
	Parts         []storedRowPart         `json:"parts,omitempty"`
}

type storedRowPart struct {
	Address PersistedPartAddress `json:"address"`
	State   string               `json:"state"`
	Kind    string               `json:"kind,omitempty"`
}

// encodeStoredRow returns the saved self payload of a stored record: header
// is its envelope as a save writes it, whose value the body holds.
func encodeStoredRow(header *PersistedResultEnvelope, body *storedBody) ([]byte, error) {
	h := storedRowHeader{Envelope: *lightStoredEnvelope(header), EnvelopeBytes: body.envelopeBytes, Refs: body.refs}
	h.Envelope.StoredParts = header.StoredParts
	for _, part := range body.parts {
		h.Parts = append(h.Parts, storedRowPart{Address: part.address, State: part.state, Kind: part.kind})
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	out := binary.AppendUvarint(append(make([]byte, 0, 1+binary.MaxVarintLen64+len(raw)+len(body.envelope)), storedRowMark), uint64(len(raw)))
	out = append(out, raw...)
	return append(out, body.envelope...), nil
}

// decodeStoredRow reads a saved stored record's self payload, as
// encodeStoredRow writes it, with the call its row saved: its envelope as the
// save wrote it, without its value, and its body.
func decodeStoredRow(payload []byte, call *ResultCall) (*PersistedResultEnvelope, *storedBody, error) {
	if !isStoredRow(payload) {
		return nil, nil, errors.New("stored row: no mark")
	}
	payload = payload[1:]
	n, read := binary.Uvarint(payload)
	if read <= 0 || n > uint64(len(payload)-read) {
		return nil, nil, errors.New("stored row: bad header length")
	}
	var h storedRowHeader
	dec := json.NewDecoder(bytes.NewReader(payload[read : read+int(n)]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return nil, nil, fmt.Errorf("stored row header: %w", err)
	}
	body := &storedBody{
		envelope:      slices.Clone(payload[read+int(n):]),
		envelopeBytes: h.EnvelopeBytes,
		call:          call,
		refs:          h.Refs,
	}
	for _, part := range h.Parts {
		key, err := partAddressKey(part.Address)
		if err != nil {
			return nil, nil, fmt.Errorf("stored row part: %w", err)
		}
		body.parts = append(body.parts, declaredPart{key: key, address: part.Address, state: part.State, kind: part.Kind})
	}
	return &h.Envelope, body, nil
}

// holdRestoredFramesCompressed has each restored stored record's body hold
// its call frame too, as the body worker would have, once the restore has
// derived the recipes from the decoded frames (indexRestoredEntries): the
// entries then keep no decoded frame. The frames are compressed on all
// cores, outside egraphMu, a batch at a time, and each batch's bodies are
// installed in one hold of it as soon as the batch is compressed, so that
// its decoded frames can go before the next batch is compressed: the pass
// adds to the restore's memory only what one batch has in flight. The
// shared encoder compresses one record at a time, so the pass has an
// encoder of its own, with a state for each core, which it lets go after.
func (c *Cache) holdRestoredFramesCompressed() error {
	type read struct {
		res  *sharedResult
		env  *PersistedResultEnvelope
		was  *storedBody
		body *storedBody
	}
	var reads []read
	c.egraphMu.RLock()
	for _, res := range c.resultsByID {
		if body := res.stored; body != nil && body.frame == nil && body.call != nil && body.call == res.loadResultCall() {
			reads = append(reads, read{res: res, env: res.persistedEnvelope, was: body})
		}
	}
	c.egraphMu.RUnlock()
	if len(reads) == 0 {
		return nil
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(storedBodyLevel), zstd.WithEncoderConcurrency(runtime.GOMAXPROCS(0)))
	if err != nil {
		return err
	}
	defer enc.Close()
	for len(reads) > 0 {
		batch := reads[:min(len(reads), storedFrameRestoreBatch)]
		reads = reads[len(batch):]
		if err := forEachIndexParallel(len(batch), func(i int) error {
			body, err := batch[i].was.withFrameEncoded(c, batch[i].was.call, enc)
			batch[i].body = body
			return err
		}); err != nil {
			return err
		}
		c.egraphMu.Lock()
		for i, r := range batch {
			if r.body != r.was && r.res.stored == r.was && c.resultsByID[r.res.id] == r.res {
				c.setStoredBodyLocked(r.res, r.env, r.body)
			}
			// The old body holds the decoded frame: nothing here keeps it.
			batch[i] = read{}
		}
		c.egraphMu.Unlock()
	}
	return nil
}

// storedFrameRestoreBatch is how many restored records holdRestoredFramesCompressed
// compresses before it installs their bodies: enough to keep every core busy
// for a while between holds of egraphMu, few enough that a batch's encodings
// in flight are small beside the restored cache.
const storedFrameRestoreBatch = 4096

// storedBodyWorker compresses the records merges installed on a blob-backed
// cache, outside every lock, and installs each body under egraphMu if the
// entry still holds the record it compressed.
type storedBodyWorker struct {
	mu      sync.Mutex
	pending []*sharedResult
	// content are the entries whose frames lack the content digest a span
	// taught them (queueTaughtContentLocked).
	content []*sharedResult
	closed  bool
	wake    chan struct{}
	done    chan struct{}
}

// startStoredBodyWorker starts a blob-backed cache's body worker.
func (c *Cache) startStoredBodyWorker() {
	w := &storedBodyWorker{wake: make(chan struct{}, 1), done: make(chan struct{})}
	c.storedBodies = w
	go func() {
		defer close(w.done)
		for range w.wake {
			for {
				w.mu.Lock()
				batch, content, closed := w.pending, w.content, w.closed
				w.pending, w.content = nil, nil
				w.mu.Unlock()
				if closed {
					return
				}
				if len(batch) == 0 && len(content) == 0 {
					break
				}
				if len(batch) > 0 {
					c.storeBodies(batch)
				}
				if len(content) > 0 {
					c.applyTaughtContent(content)
				}
			}
		}
	}()
}

// queueStoredBodies has the body worker compress the entries' records.
func (c *Cache) queueStoredBodies(entries []*sharedResult) {
	w := c.storedBodies
	if w == nil || len(entries) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.pending = append(w.pending, entries...)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// queueTaughtContentLocked has the body worker give res's frame the content
// digests spans taught res that the frame hasn't taken, when res has a frame.
// A frame known to carry each of them as its content digest takes nothing:
// one decoded since with that content digest is. A frame a restored body
// holds is not known until it is decoded. Requires egraphMu.
func (c *Cache) queueTaughtContentLocked(res *sharedResult) {
	w := c.storedBodies
	seeded := res.taughtFrom.ok()
	if !c.blobBacked || w == nil || (res.taughtGiven == len(res.taught) && !seeded) || !res.entryFrameLocked().ok() {
		return
	}
	res.resultCallMu.RLock()
	known, carried := res.frameContentKnown, res.frameContent
	res.resultCallMu.RUnlock()
	if !seeded && known && !slices.ContainsFunc(res.taught[res.taughtGiven:], func(d digest.Digest) bool { return d != carried }) {
		c.taughtContentCarried.Add(uint64(len(res.taught) - res.taughtGiven))
		res.taughtGiven = len(res.taught)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.content = append(w.content, res)
	c.taughtContentQueued.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// applyTaughtContent gives each entry's frame the content digest a span
// taught it, as TeachContentDigest gives an engine's result's frame, outside
// egraphMu but for the replacement: the frame, or the body holding it, is
// decoded and rewritten off the lock, once, and replaced in one short hold if
// it hasn't changed since. An entry collected, or whose frame carries the
// digest by then, is left as it is.
func (c *Cache) applyTaughtContent(entries []*sharedResult) {
	ctx := context.Background()
	rewritten := 0
	start := time.Now()
	for _, res := range entries {
		c.adoptReplacedContent(res)
		c.egraphMu.RLock()
		from, frame, live := res.taughtGiven, res.taughtFrame, c.resultsByID[res.id] == res
		pending := slices.Clone(res.taught[from:])
		c.egraphMu.RUnlock()
		if !live || len(pending) == 0 {
			continue
		}
		applied := 0
		for _, content := range pending {
			rewrote, err := c.teachContentDigest(ctx, res, content, nil, true)
			if err != nil {
				slog.Warn("dagql cache: giving an entry's frame its taught content digest", "entry", res.id, "err", err)
				break
			}
			applied++
			if rewrote {
				rewritten++
			} else {
				c.taughtContentCarried.Add(1)
			}
		}
		if c.testTaughtContentGiven != nil {
			c.testTaughtContentGiven()
		}
		c.egraphMu.Lock()
		// A frame a replacement installed since has taken none of them, and
		// its queueing gives it them all. Digests taught meanwhile are
		// given by their own queueing.
		if res.taughtFrame == frame && res.taughtGiven == from {
			res.taughtGiven = from + applied
		}
		c.egraphMu.Unlock()
	}
	c.taughtContentRewrites.Add(uint64(rewritten))
	c.taughtContentNanos.Add(int64(time.Since(start)))
	if c.testTaughtContent != nil {
		c.testTaughtContent(rewritten)
	}
}

// queueRestoredTaughtContent has the body worker give the entries a restore
// left with taught content digests their saved frame hadn't taken (a save
// before the worker gave them) the rest.
func (c *Cache) queueRestoredTaughtContent() {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	for _, res := range c.restoredTaught {
		c.queueTaughtContentLocked(res)
	}
	c.restoredTaught = nil
}

// adoptReplacedContent takes, as res's taught content digests, those of the
// frame a replacement took from res when res had none (taughtFrom): its
// replaced content digests in the order the frame kept them, then its
// content digest. The frame is decoded outside egraphMu.
func (c *Cache) adoptReplacedContent(res *sharedResult) {
	c.egraphMu.RLock()
	from := res.taughtFrom
	c.egraphMu.RUnlock()
	if !from.ok() {
		return
	}
	var run []digest.Digest
	if frame, err := from.decode(); err != nil {
		slog.Warn("dagql cache: reading a replaced frame's content digests", "entry", res.id, "err", err)
	} else if frame != nil {
		for _, extra := range frame.ExtraDigests {
			if extra.Label == call.ExtraDigestLabelReplacedContent && extra.Digest != "" {
				run = append(run, extra.Digest)
			}
		}
		if content := frame.ContentDigest(); content != "" {
			run = append(run, content)
		}
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	if res.taughtFrom != from {
		return
	}
	res.taughtFrom = entryFrame{}
	// Digests taught since the replacement come after the frame's.
	res.taught = append(run, res.taught...)
}

// TaughtContentStats counts, since the cache opened, what giving entries'
// frames the content digests spans taught them did (queueTaughtContentLocked,
// applyTaughtContent).
type TaughtContentStats struct {
	// Queued are the entries queued for a rewrite: a span taught a digest
	// their frame wasn't known to carry.
	Queued uint64
	// Rewritten are the frames rewritten.
	Rewritten uint64
	// Carried are the taught digests found already on the frame, when
	// queued or when the worker decoded it.
	Carried uint64
	// BundlesWaiting are the bundle builds that found an entry, sent or
	// held, whose frame wasn't yet recorded as having taken all its taught
	// digests: an upper bound on the bundles that sent a frame before its
	// rewrite, since a frame can be rewritten before that is recorded, and
	// a build can still fail after counting.
	BundlesWaiting uint64
	// Took is the time the rewrites took.
	Took time.Duration
}

// TaughtContentStats reports what giving frames their taught content digests
// has done since the cache opened.
func (c *Cache) TaughtContentStats() TaughtContentStats {
	return TaughtContentStats{
		Queued:         c.taughtContentQueued.Load(),
		Rewritten:      c.taughtContentRewrites.Load(),
		Carried:        c.taughtContentCarried.Load(),
		BundlesWaiting: c.taughtContentBundlesWaiting.Load(),
		Took:           time.Duration(c.taughtContentNanos.Load()),
	}
}

// waitsForTaughtContentLocked reports whether res's frame has yet to take a
// content digest a span taught res. Requires egraphMu.
func (res *sharedResult) waitsForTaughtContentLocked() bool {
	return res.taughtGiven < len(res.taught) || res.taughtFrom.ok()
}

// closeStoredBodies stops the body worker. Entries it hadn't compressed keep
// their whole envelopes, which a save compresses.
func (c *Cache) closeStoredBodies() {
	w := c.storedBodies
	if w == nil {
		return
	}
	w.mu.Lock()
	content := w.content
	if !w.closed {
		w.closed = true
		w.pending, w.content = nil, nil
		close(w.wake)
	}
	w.mu.Unlock()
	<-w.done
	// A save keeps no taught digest a frame hasn't taken (taught is not
	// saved), so the frames take theirs before the cache closes.
	if len(content) > 0 {
		c.applyTaughtContent(content)
	}
}

// storeBodies compresses each entry's record, as it stands when read, and
// installs the bodies in one hold of egraphMu, on the entries that still hold
// the records compressed. A record that fails to compress stays whole: its
// readers normalize it, and report its error, as they would have.
func (c *Cache) storeBodies(entries []*sharedResult) {
	type read struct {
		res  *sharedResult
		env  *PersistedResultEnvelope
		call *ResultCall
		body *storedBody
	}
	reads := make([]read, 0, len(entries))
	c.egraphMu.RLock()
	for _, res := range entries {
		if res.stored == nil && res.persistedEnvelope != nil && c.resultsByID[res.id] == res {
			reads = append(reads, read{res: res, env: res.persistedEnvelope, call: res.loadResultCall()})
		}
	}
	c.egraphMu.RUnlock()
	for i := range reads {
		body, err := newStoredBody(reads[i].res, reads[i].env, reads[i].call)
		if err == nil {
			// The frame goes into the body too, with its recipe digest,
			// which deriving reads from the cache: no lock is held here.
			body, err = body.withFrame(c, reads[i].call)
		}
		if err == nil {
			reads[i].body = body
		}
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	for _, r := range reads {
		if r.body != nil && r.res.stored == nil && r.res.persistedEnvelope == r.env && r.res.loadResultCall() == r.call && c.resultsByID[r.res.id] == r.res {
			c.setStoredBodyLocked(r.res, r.env, r.body)
		}
	}
	if c.testStoredBodies != nil {
		c.testStoredBodies(len(reads))
	}
}
