package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/opencontainers/go-digest"
)

// StoredSize is what a root adds to a stored bundle: its values, and the sum
// of their encoded sizes, each as json.Marshal encodes a TransferredValue;
// and what its closure's outputs add to the message a merge of the bundle is
// sent in.
type StoredSize struct {
	Records int
	Bytes   int64
	// OutputBytes counts the outputs as the message carries them
	// (ShareChainLayers): each of the closure's own stored parts without its
	// layers (storedPartWire), each layer the bundle doesn't list yet
	// (storedLayerWire), and storedBlobAddressAllowance for each download
	// address it adds, which signing adds when the merge is sent: one for
	// each blob, and one for each output that names a blob in another pack
	// than the bundle's address is for (storedOutputsSeen). It is never less than the message carries for them, but
	// for an address over the allowance. Parts a closure entry borrows from
	// another entry of its class aren't counted: the merge carries them too.
	OutputBytes int64
	// Unmeasured marks a root MeasuredStoredSizes didn't measure: its
	// closure has a value no measure has sized yet.
	Unmeasured bool
}

// heldRowSizeDigest stands for a held row's recipe when the entry has no
// recipe key to measure by: an xxh3 digest, as recipes are.
var heldRowSizeDigest = digest.Digest("xxh3:0000000000000000")

// StoredSizes measures what StoredBundle(roots, nowUnix, held) carries
// without building it, judging held's holdings at nowUnix, the reader's
// clock: for each root, in order, what its closure adds beyond the
// closures of the roots before it, an entry held holds counted as its held
// row. A root's closure includes the entries StoredBundle adds for the inputs
// of its entries' kept calls, priced so that the sizes of the first roots
// are never less than what a bundle of them carries (walkStoredRoot). A root
// the cache has no value for, or one an earlier root's closure already
// holds, adds nothing; so does an entry of a closure with no stored record,
// and nothing below it is walked.
//
// Each entry's value is encoded once, with its references at entry numbers,
// and its size memoized with its normalized record (normalizeStoredTransfer)
// until the record, its dependencies, replacement count or expiry change. A
// held row's recipe is measured by one of the entry's recipe keys. A bundle
// numbers references by ordinal instead, so a size differs from the bundle's
// encoding by the digits of each reference.
//
// For each root in turn it walks the closure under egraphMu.RLock, adding the
// sizes already measured and reading the value state of each entry not yet
// measured; it normalizes and encodes those after releasing the lock, and
// keeps each size under the lock again only for an entry whose value is
// still the one read. A hold is the walk, or that check, alone. Only a
// blob-backed cache answers.
func (c *Cache) StoredSizes(ctx context.Context, roots []uint64, nowUnix int64, held *HeldBy) ([]StoredSize, error) {
	return c.storedSizes(ctx, roots, nowUnix, held, true)
}

// MeasuredStoredSizes answers as StoredSizes does without measuring anything:
// a root whose closure, beyond the measured roots before it, has a value not
// yet measured is answered Unmeasured, with no records or bytes, and the
// later roots are measured as if it weren't there. It normalizes and encodes
// nothing: a held row's size is computed (heldRowSize).
func (c *Cache) MeasuredStoredSizes(ctx context.Context, roots []uint64, nowUnix int64, held *HeldBy) ([]StoredSize, error) {
	return c.storedSizes(ctx, roots, nowUnix, held, false)
}

// StoredSizeEncodes counts the values StoredSizes has measured: each first
// measurement of a value's record, dependencies, replacement count and
// expiry.
func (c *Cache) StoredSizeEncodes() uint64 {
	return c.storedSizeEncodes.Load()
}

func (c *Cache) storedSizes(ctx context.Context, roots []uint64, nowUnix int64, held *HeldBy, measure bool) ([]StoredSize, error) {
	if !c.blobBacked {
		return nil, fmt.Errorf("stored sizes: the cache has no blob store")
	}
	sizes := make([]StoredSize, len(roots))
	seen, ordinary := map[sharedResultID]bool{}, map[sharedResultID]bool{}
	outputs := &storedOutputsSeen{layers: map[*storedLayer]bool{}, blobs: map[digest.Digest]digest.Digest{}}
	for i, number := range roots {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		size, closure, own, misses := c.walkStoredRoot(sharedResultID(number), nowUnix, held, seen, ordinary, outputs)
		if len(misses) > 0 && !measure {
			sizes[i] = StoredSize{Unmeasured: true}
			continue
		}
		measured := make([]storedSizeMeasure, 0, len(misses))
		for _, miss := range misses {
			m, err := c.measureStoredValue(miss)
			if err != nil {
				return nil, fmt.Errorf("stored sizes: entry %d: %w", miss.res.id, err)
			}
			size.Bytes += m.size.bytes
			measured = append(measured, m)
		}
		c.publishStoredSizes(measured)
		for _, id := range closure {
			seen[id] = true
		}
		for _, id := range closure[:own] {
			ordinary[id] = true
		}
		sizes[i] = size
	}
	return sizes, nil
}

// storedSizeMiss is a value StoredSizes measures after releasing egraphMu:
// its entry, and the value state the walk read under the lock.
type storedSizeMiss struct {
	res  *sharedResult
	env  *PersistedResultEnvelope
	body *storedBody
	call *ResultCall
	// The value state its size depends on.
	deps               []uint64
	dependencyRevision uint64
	replacements       uint64
	expiresAtUnix      int64
}

// storedSizesRLock takes egraphMu for reading and returns its release, which
// reports the hold to testStoredSizesHold.
func (c *Cache) storedSizesRLock() func() {
	c.egraphMu.RLock()
	locked := time.Now()
	return func() {
		hold := time.Since(locked)
		c.egraphMu.RUnlock()
		if c.testStoredSizesHold != nil {
			c.testStoredSizesHold(hold)
		}
	}
}

// walkStoredRoot walks what root's closure adds beyond seen, in one hold of
// egraphMu.RLock: the entries it adds, the size of those already measured or
// held, counting every value, and the values not yet measured; and how many
// of the entries, first, are its ordinary closure. It walks as StoredBundle
// builds (storedBundleVisitLocked, storedBundleCallsLocked): the closure,
// dependencies first, then, entry by entry in that order, the calls kept on
// each, adding the closure of each input a chosen call needs.
//
// The price is never less than what a bundle of the roots up to this one
// adds for it. Which of a term's calls a bundle chooses depends on what the
// whole bundle carries, which a later root's closure can change: so a call
// counts as placed only when the ordinary closures of the roots so far
// (ordinary), or the recipient (held), have its inputs, never for an input
// another call adds. Otherwise its first stored alternative's inputs are
// priced, which is the call a bundle that can't place it chooses too.
func (c *Cache) walkStoredRoot(root sharedResultID, nowUnix int64, held *HeldBy, seen, ordinary map[sharedResultID]bool, outputs *storedOutputsSeen) (StoredSize, []sharedResultID, int, []storedSizeMiss) {
	defer c.storedSizesRLock()()
	res := c.resultsByID[root]
	if res == nil || res.noValueLocked() {
		return StoredSize{}, nil, 0, nil
	}
	walk, own := c.storedRootWalkLocked(res, nowUnix, held, seen, ordinary, outputs)
	return walk.size, walk.closure, own, walk.misses
}

// storedRootWalkLocked is walkStoredRoot's walk of res, and how many of the
// entries it reached, first, are res's ordinary closure. Requires egraphMu.
func (c *Cache) storedRootWalkLocked(res *sharedResult, nowUnix int64, held *HeldBy, seen, ordinary map[sharedResultID]bool, outputs *storedOutputsSeen) (*storedSizeWalk, int) {
	walk := &storedSizeWalk{c: c, held: held, now: nowUnix, seen: seen, visited: map[sharedResultID]bool{}, outputs: outputs}
	walk.visit(res)
	own := len(walk.closure)
	mine := make(map[sharedResultID]bool, own)
	for _, id := range walk.closure {
		mine[id] = true
	}
	for i := 0; i < len(walk.order); i++ {
		entry := walk.order[i]
		if len(entry.calls) == 0 {
			continue
		}
		for _, group := range c.keptCallTermsLocked(entry) {
			_, add := c.placeableCallLocked(group, func(input *sharedResult) bool {
				return ordinary[input.id] || mine[input.id] || held.heldLocked(input, nowUnix)
			})
			for _, input := range add {
				walk.visit(input)
			}
		}
	}
	return walk, own
}

// storedSizeWalk is walkStoredRoot's walk: the entries it reached
// (visited), those it adds, in the order StoredBundle adds them (order), and
// what they add up to.
type storedSizeWalk struct {
	c       *Cache
	held    *HeldBy
	now     int64
	seen    map[sharedResultID]bool
	visited map[sharedResultID]bool
	order   []*sharedResult
	size    StoredSize
	closure []sharedResultID
	misses  []storedSizeMiss
	outputs *storedOutputsSeen
}

// storedOutputsSeen are the layers a bundle's outputs list so far, which a
// merge's message carries once (ShareChainLayers), and the blobs whose
// address it carries, with the pack the first was signed for: a packed
// layer's address is its pack's, so a blob in another pack, or in none, has
// another address, which the message carries on the chain of each output
// that names it.
type storedOutputsSeen struct {
	layers map[*storedLayer]bool
	blobs  map[digest.Digest]digest.Digest
}

// addOutputs counts what res's own stored parts add to the message
// (StoredSize's OutputBytes), in the order a bundle adds them, by key
// (storedBundleOutputsLocked): which of a blob's addresses the message
// shares depends on it. Requires egraphMu.
func (w *storedSizeWalk) addOutputs(res *sharedResult) {
	for _, key := range slices.Sorted(maps.Keys(res.storedParts)) {
		part := res.storedParts[key]
		w.size.OutputBytes += part.wire
		for _, layer := range part.layers {
			if !w.outputs.layers[layer] {
				w.outputs.layers[layer] = true
				w.size.OutputBytes += layer.wire
			}
			blob := layer.layer.Descriptor.Digest
			if pack, ok := w.outputs.blobs[blob]; !ok {
				w.outputs.blobs[blob] = layer.pack
				w.size.OutputBytes += storedBlobAddressAllowance
			} else if pack != layer.pack {
				w.size.OutputBytes += storedBlobAddressAllowance
			}
		}
	}
}

// carries reports whether the bundle carries the entry: an earlier root's
// closure or this walk has it.
func (w *storedSizeWalk) carries(id sharedResultID) bool {
	return w.seen[id] || w.visited[id]
}

// visit adds res's closure, dependencies first, as storedBundleVisitLocked
// does: a held row alone, with nothing below it, and an entry with no
// stored record not at all. Requires egraphMu.
func (w *storedSizeWalk) visit(res *sharedResult) {
	if w.carries(res.id) {
		return
	}
	w.visited[res.id] = true
	w.closure = append(w.closure, res.id)
	if w.held.heldLocked(res, w.now) {
		w.size.Records++
		w.size.Bytes += heldRowSizeLocked(res)
		w.order = append(w.order, res)
		return
	}
	if res.noValueLocked() || res.persistedEnvelope == nil {
		return
	}
	for _, id := range storedClosureChildrenLocked(res) {
		if child := w.c.resultsByID[id]; child != nil {
			w.visit(child)
		}
	}
	w.size.Records++
	w.addOutputs(res)
	if bytes, ok := storedValueMeasuredLocked(res); ok {
		w.size.Bytes += bytes
	} else {
		w.misses = append(w.misses, storedSizeMissLocked(res))
	}
	w.order = append(w.order, res)
}

// storedValueMeasuredLocked returns res's measured size, if its record,
// dependencies, replacement count and expiry are the ones measured. Requires
// egraphMu.
func storedValueMeasuredLocked(res *sharedResult) (int64, bool) {
	var size *storedValueSize
	if body := res.stored; body != nil {
		if size = body.size.Load(); size != nil && size.call != res.loadResultCall() {
			return 0, false
		}
	} else {
		kept := res.storedTransfer.Load()
		if kept == nil || kept.envelope != res.persistedEnvelope || kept.call != res.loadResultCall() {
			return 0, false
		}
		size = kept.size.Load()
	}
	if size == nil || size.dependencyRevision != res.dependencyOwnershipRevision || size.replacements != res.replacements || size.expiresAtUnix != res.expiresAtUnix {
		return 0, false
	}
	return size.bytes, true
}

// storedSizeMissLocked reads the value state of res that its size depends on.
// Requires egraphMu.
func storedSizeMissLocked(res *sharedResult) storedSizeMiss {
	miss := storedSizeMiss{res: res, env: res.persistedEnvelope, body: res.stored, call: res.loadResultCall(), dependencyRevision: res.dependencyOwnershipRevision, replacements: res.replacements, expiresAtUnix: res.expiresAtUnix}
	for dep := range res.deps {
		miss.deps = append(miss.deps, uint64(dep))
	}
	slices.Sort(miss.deps)
	return miss
}

// storedSizeMeasure is a value measured outside egraphMu: its transfer
// record and size, for the value state its walk read.
type storedSizeMeasure struct {
	miss storedSizeMiss
	kept *storedTransferRecord
	size *storedValueSize
}

// measureStoredValue measures a value as the walk read it, without egraphMu:
// it normalizes the record (normalizeStoredTransfer) and encodes the value
// with its references at entry numbers. It keeps nothing: publishStoredSizes
// does, under the lock.
func (c *Cache) measureStoredValue(miss storedSizeMiss) (storedSizeMeasure, error) {
	if c.testStoredSizeMeasure != nil {
		c.testStoredSizeMeasure()
	}
	value := TransferredValue{Ordinal: TransferOrdinal(miss.res.id), SenderNumber: uint64(miss.res.id), SenderReplacements: miss.replacements, DependencyIDs: miss.deps, ExpiresAtUnix: miss.expiresAtUnix}
	var kept *storedTransferRecord
	var bytes int64
	if miss.body != nil {
		// A body's value is measured with an empty envelope in its place,
		// and its envelope's own length added: nothing is decompressed.
		call, err := normalizeTransferCall(miss.call)
		if err != nil {
			return storedSizeMeasure{}, err
		}
		value.Record = PersistedRecord{ResultID: uint64(miss.res.id), Call: call}
		empty, err := encodedSize(value)
		if err != nil {
			return storedSizeMeasure{}, err
		}
		bytes = empty - emptyEnvelopeBytes + miss.body.envelopeBytes
	} else {
		var err error
		if kept, err = normalizeStoredTransfer(miss.res, miss.env, miss.call); err != nil {
			return storedSizeMeasure{}, err
		}
		value.Record = kept.record
		if bytes, err = encodedSize(value); err != nil {
			return storedSizeMeasure{}, err
		}
	}
	c.storedSizeEncodes.Add(1)
	return storedSizeMeasure{miss: miss, kept: kept, size: &storedValueSize{call: miss.call, dependencyRevision: miss.dependencyRevision, replacements: miss.replacements, expiresAtUnix: miss.expiresAtUnix, bytes: bytes}}, nil
}

// publishStoredSizes keeps each measure's size on its entry, in one hold of
// egraphMu.RLock, if the entry is still in the state measured: the same
// envelope and call, dependencies, replacement count and expiry. An entry
// changed since then keeps nothing, so a late measure never brings back a
// record the cache let go, nor replaces a size measured for a newer state.
// The size goes on the record kept for that envelope and call: the
// measure's own, or one a build kept meanwhile (keepStoredTransfer).
func (c *Cache) publishStoredSizes(measured []storedSizeMeasure) {
	if len(measured) == 0 {
		return
	}
	defer c.storedSizesRLock()()
	for _, m := range measured {
		res, miss := m.miss.res, m.miss
		if res.persistedEnvelope != miss.env || res.stored != miss.body || res.loadResultCall() != miss.call || res.dependencyOwnershipRevision != miss.dependencyRevision || res.replacements != miss.replacements || res.expiresAtUnix != miss.expiresAtUnix {
			continue
		}
		if miss.body != nil {
			miss.body.size.Store(m.size)
			continue
		}
		keepStoredTransfer(res, m.kept).size.Store(m.size)
	}
}

// emptyEnvelopeBytes is the length of an empty envelope's encoding, which a
// body's measure replaces with its envelope's.
var emptyEnvelopeBytes = func() int64 {
	raw, err := json.Marshal(PersistedResultEnvelope{})
	if err != nil {
		panic(err)
	}
	return int64(len(raw))
}()

// sizeCounter is an io.Writer that counts what is written to it.
type sizeCounter int64

func (n *sizeCounter) Write(p []byte) (int, error) {
	*n += sizeCounter(len(p))
	return len(p), nil
}

// encodedSize returns the length of v's json.Marshal encoding, counted
// through an Encoder, whose buffer is pooled, rather than allocated whole.
func encodedSize(v any) (int64, error) {
	var n sizeCounter
	if err := json.NewEncoder(&n).Encode(v); err != nil {
		return 0, err
	}
	// Encode ends the value with a newline that Marshal doesn't write.
	return int64(n) - 1, nil
}

// heldRowSizeLocked returns the encoded size of res as a held row, its
// recipe measured by one of its recipe keys: StoredBundle derives the recipe
// from its call frame after the lock, and recipes are of one length.
// Requires egraphMu.
func heldRowSizeLocked(res *sharedResult) int64 {
	recipe := heldRowSizeDigest
	if len(res.recipeKeys) > 0 {
		recipe = res.recipeKeys[0]
	}
	return heldRowSize(uint64(res.id), res.replacements, res.expiresAtUnix, recipe)
}

// heldRowEncoding is what a held row's json.Marshal encoding is made of:
// a row with every field zero, and what a nonzero replacement count, expiry
// and recipe each add beyond their digits or characters.
var heldRowEncoding = func() (enc struct{ base, replacements, expires, held int64 }) {
	size := func(v TransferredValue) int64 {
		data, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		return int64(len(data))
	}
	enc.base = size(TransferredValue{})
	enc.replacements = size(TransferredValue{SenderReplacements: 1}) - enc.base - 1
	enc.expires = size(TransferredValue{ExpiresAtUnix: 1}) - enc.base - 1
	enc.held = size(TransferredValue{Held: "x"}) - enc.base - 1
	return enc
}()

// heldRowSize returns the length of the json.Marshal encoding of the held row
// StoredBundle builds for entry number with the replacement count, expiry and
// recipe, without encoding it: its ordinal, sender number and record's
// result ID are the number, and a recipe has no character JSON escapes.
func heldRowSize(number, replacements uint64, expiresAtUnix int64, recipe digest.Digest) int64 {
	size := heldRowEncoding.base + 3*(decimalDigits(number)-1) + heldRowEncoding.held + int64(len(recipe))
	if replacements != 0 {
		size += heldRowEncoding.replacements + decimalDigits(replacements)
	}
	if expiresAtUnix < 0 {
		size += heldRowEncoding.expires + 1 + decimalDigits(uint64(-expiresAtUnix))
	} else if expiresAtUnix > 0 {
		size += heldRowEncoding.expires + decimalDigits(uint64(expiresAtUnix))
	}
	return size
}

// decimalDigits returns the number of decimal digits of n.
func decimalDigits(n uint64) int64 {
	digits := int64(1)
	for ; n >= 10; n /= 10 {
		digits++
	}
	return digits
}

// storedBlobAddressAllowance is what a price allows for each blob's download
// address in a merge's message (BlobAddress), which signing adds: a presigned
// URL and its expiry, which run to about 1.5 KiB with a session token.
// storedOutputReferenceSlack bounds what a price counts for an output beyond
// a bundle built without addresses: the renewal key signing adds, and its
// references at storedWireReference's digits.
const (
	storedBlobAddressAllowance = 2 << 10
	storedOutputReferenceSlack = 96
)

// StoredLowerHalf returns what goes ahead of a merge of root whose message
// would be over the limit, as the service's split of a merge refused as over
// it (its lowerHalf) chooses from the bundle: of the entries a bundle of root
// carries (StoredBundle, its kept calls' inputs included), but the entries
// held holds, those below root, not root nor an entry that depends on it, as
// a call's input can; the first half of them in the bundle's order,
// dependencies first; and of those the ones no other of them depends on.
// Once the recipient holds them, a merge of root carries that half as held
// rows. It also returns how many records, but held rows, a bundle of root
// carries, as that split counts them. A root the cache has no value for has
// none.
func (c *Cache) StoredLowerHalf(ctx context.Context, root uint64, nowUnix int64, held *HeldBy) ([]uint64, int, error) {
	if !c.blobBacked {
		return nil, 0, fmt.Errorf("stored lower half: the cache has no blob store")
	}
	if err := context.Cause(ctx); err != nil {
		return nil, 0, err
	}
	defer c.storedSizesRLock()()
	res := c.resultsByID[sharedResultID(root)]
	if res == nil || res.noValueLocked() {
		return nil, 0, nil
	}
	walk, _ := c.storedRootWalkLocked(res, nowUnix, held, map[sharedResultID]bool{}, map[sharedResultID]bool{},
		&storedOutputsSeen{layers: map[*storedLayer]bool{}, blobs: map[digest.Digest]digest.Digest{}})
	above := map[sharedResultID]bool{res.id: true}
	var below []*sharedResult
	carried := 0
	for _, entry := range walk.order {
		if held.heldLocked(entry, nowUnix) {
			continue
		}
		carried++
		if !above[entry.id] && slices.ContainsFunc(storedClosureChildrenLocked(entry), func(id sharedResultID) bool { return above[id] }) {
			above[entry.id] = true
		}
		if !above[entry.id] {
			below = append(below, entry)
		}
	}
	half := below[:(len(below)+1)/2]
	under := map[sharedResultID]bool{}
	for _, entry := range half {
		for _, dep := range storedClosureChildrenLocked(entry) {
			under[dep] = true
		}
	}
	var lower []uint64
	for _, entry := range half {
		if !under[entry.id] {
			lower = append(lower, uint64(entry.id))
		}
	}
	return lower, carried, nil
}
