package dagql

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
)

// A blob-backed cache, the Cloud, never decodes a stored value: it indexes,
// chooses, sends and saves records. So it holds each stored record's envelope
// in transfer form as compressed JSON (storedBody), beside the facts its
// readers need without it: the entries its references name, which a bundle
// checks, and the parts it declares absent or pending, which part answers and
// borrowing read. Its persistedEnvelope keeps only the envelope's small
// fields (lightStoredEnvelope), which tell a stored value by its presence and
// its kind. The call frame stays decoded on the entry, since identity reads
// it.
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
	// call is the frame the body was made with, and refs and parts are of
	// the two: the entries the record's child and call references name,
	// sorted, and the parts it declares absent or pending. The entry's call
	// can be replaced after (storedFactsLocked).
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
// envelope of env, and no kept transfer record. Requires egraphMu for
// writing, or an entry no reader can reach yet.
func (c *Cache) setStoredBodyLocked(res *sharedResult, env *PersistedResultEnvelope, body *storedBody) {
	// A size measured for the same record and call is the body's: a value is
	// measured the same whole or compressed.
	if kept := res.storedTransfer.Load(); kept != nil && kept.envelope == env && kept.call == body.call {
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
		rec.Call = res.loadResultCall()
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
// body's, unless its call was replaced since the body was made, as teaching
// it a digest does: then they are worked out again from the body and the
// call. Requires egraphMu.
func (res *sharedResult) storedFactsLocked() ([]uint64, []declaredPart, error) {
	body := res.stored
	call := res.loadResultCall()
	if body.call == call {
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

// storedBodyWorker compresses the records merges installed on a blob-backed
// cache, outside every lock, and installs each body under egraphMu if the
// entry still holds the record it compressed.
type storedBodyWorker struct {
	mu      sync.Mutex
	pending []*sharedResult
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
				batch, closed := w.pending, w.closed
				w.pending = nil
				w.mu.Unlock()
				if closed {
					return
				}
				if len(batch) == 0 {
					break
				}
				c.storeBodies(batch)
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

// closeStoredBodies stops the body worker. Entries it hadn't compressed keep
// their whole envelopes, which a save compresses.
func (c *Cache) closeStoredBodies() {
	w := c.storedBodies
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		w.pending = nil
		close(w.wake)
	}
	w.mu.Unlock()
	<-w.done
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
