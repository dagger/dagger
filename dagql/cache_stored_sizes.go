package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/opencontainers/go-digest"
)

// StoredSize is what a root adds to a stored bundle: its values, and the sum
// of their encoded sizes, each as json.Marshal encodes a TransferredValue.
type StoredSize struct {
	Records int
	Bytes   int64
	// Unmeasured marks a root MeasuredStoredSizes didn't measure: its
	// closure has a value no measure has sized yet.
	Unmeasured bool
}

// heldRowSizeDigest stands for a held row's recipe when the entry has no
// recipe key to measure by: an xxh3 digest, as recipes are.
var heldRowSizeDigest = digest.Digest("xxh3:0000000000000000")

// StoredSizes measures what StoredBundle(roots, held) carries without
// building it: for each root, in order, what its closure adds beyond the
// closures of the roots before it, an entry held holds counted as its held
// row. A root the cache has no value for, or one an earlier root's closure
// already holds, adds nothing; so does an entry of a closure with no stored
// record, and nothing below it is walked.
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
func (c *Cache) StoredSizes(ctx context.Context, roots []uint64, held *HeldBy) ([]StoredSize, error) {
	return c.storedSizes(ctx, roots, held, true)
}

// MeasuredStoredSizes answers as StoredSizes does without measuring anything:
// a root whose closure, beyond the measured roots before it, has a value not
// yet measured is answered Unmeasured, with no records or bytes, and the
// later roots are measured as if it weren't there. It normalizes and encodes
// nothing: a held row's size is computed (heldRowSize).
func (c *Cache) MeasuredStoredSizes(ctx context.Context, roots []uint64, held *HeldBy) ([]StoredSize, error) {
	return c.storedSizes(ctx, roots, held, false)
}

// StoredSizeEncodes counts the values StoredSizes has measured: each first
// measurement of a value's record, dependencies, replacement count and
// expiry.
func (c *Cache) StoredSizeEncodes() uint64 {
	return c.storedSizeEncodes.Load()
}

func (c *Cache) storedSizes(ctx context.Context, roots []uint64, held *HeldBy, measure bool) ([]StoredSize, error) {
	if !c.blobBacked {
		return nil, fmt.Errorf("stored sizes: the cache has no blob store")
	}
	sizes := make([]StoredSize, len(roots))
	seen := map[sharedResultID]bool{}
	for i, number := range roots {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		size, closure, misses := c.walkStoredRoot(sharedResultID(number), held, seen)
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
		sizes[i] = size
	}
	return sizes, nil
}

// storedSizeMiss is a value StoredSizes measures after releasing egraphMu:
// its entry, and the value state the walk read under the lock.
type storedSizeMiss struct {
	res                *sharedResult
	env                *PersistedResultEnvelope
	call               *ResultCall
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
// held, counting every value, and the values not yet measured.
func (c *Cache) walkStoredRoot(root sharedResultID, held *HeldBy, seen map[sharedResultID]bool) (StoredSize, []sharedResultID, []storedSizeMiss) {
	defer c.storedSizesRLock()()
	var (
		size    StoredSize
		closure []sharedResultID
		misses  []storedSizeMiss
	)
	res := c.resultsByID[root]
	if res == nil || res.noValueLocked() {
		return size, nil, nil
	}
	visited := map[sharedResultID]bool{}
	stack := []*sharedResult{res}
	for len(stack) > 0 {
		res := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[res.id] || visited[res.id] {
			continue
		}
		visited[res.id] = true
		closure = append(closure, res.id)
		if held.heldLocked(res) {
			size.Records++
			size.Bytes += heldRowSizeLocked(res)
			continue
		}
		if res.noValueLocked() || res.persistedEnvelope == nil {
			continue
		}
		size.Records++
		if bytes, ok := storedValueMeasuredLocked(res); ok {
			size.Bytes += bytes
		} else {
			misses = append(misses, storedSizeMissLocked(res))
		}
		for _, id := range storedClosureChildrenLocked(res) {
			if child := c.resultsByID[id]; child != nil && !seen[id] && !visited[id] {
				stack = append(stack, child)
			}
		}
	}
	return size, closure, misses
}

// storedValueMeasuredLocked returns res's measured size, if its record,
// dependencies, replacement count and expiry are the ones measured. Requires
// egraphMu.
func storedValueMeasuredLocked(res *sharedResult) (int64, bool) {
	kept := res.storedTransfer.Load()
	if kept == nil || kept.envelope != res.persistedEnvelope || kept.call != res.loadResultCall() {
		return 0, false
	}
	size := kept.size.Load()
	if size == nil || size.dependencyRevision != res.dependencyOwnershipRevision || size.replacements != res.replacements || size.expiresAtUnix != res.expiresAtUnix {
		return 0, false
	}
	return size.bytes, true
}

// storedSizeMissLocked reads the value state of res that its size depends on.
// Requires egraphMu.
func storedSizeMissLocked(res *sharedResult) storedSizeMiss {
	miss := storedSizeMiss{res: res, env: res.persistedEnvelope, call: res.loadResultCall(), dependencyRevision: res.dependencyOwnershipRevision, replacements: res.replacements, expiresAtUnix: res.expiresAtUnix}
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
	kept, err := normalizeStoredTransfer(miss.res, miss.env, miss.call)
	if err != nil {
		return storedSizeMeasure{}, err
	}
	bytes, err := encodedSize(TransferredValue{Ordinal: TransferOrdinal(miss.res.id), SenderNumber: uint64(miss.res.id), SenderReplacements: miss.replacements, Record: kept.record, DependencyIDs: miss.deps, ExpiresAtUnix: miss.expiresAtUnix})
	if err != nil {
		return storedSizeMeasure{}, err
	}
	c.storedSizeEncodes.Add(1)
	return storedSizeMeasure{miss: miss, kept: kept, size: &storedValueSize{dependencyRevision: miss.dependencyRevision, replacements: miss.replacements, expiresAtUnix: miss.expiresAtUnix, bytes: bytes}}, nil
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
		if res.persistedEnvelope != miss.env || res.loadResultCall() != miss.call || res.dependencyOwnershipRevision != miss.dependencyRevision || res.replacements != miss.replacements || res.expiresAtUnix != miss.expiresAtUnix {
			continue
		}
		keepStoredTransfer(res, m.kept).size.Store(m.size)
	}
}

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
// from its call frame after the lock, and recipes are of one length. The row
// carries the frame's transfer terms too (HeldTerms), which are encoded to
// measure them. Requires egraphMu.
func heldRowSizeLocked(res *sharedResult) int64 {
	recipe := heldRowSizeDigest
	if len(res.recipeKeys) > 0 {
		recipe = res.recipeKeys[0]
	}
	size := heldRowSize(uint64(res.id), res.replacements, res.expiresAtUnix, recipe)
	if frame := res.loadResultCall(); frame != nil && len(frame.TransferTerms) > 0 {
		size += heldTermsSize(frame.TransferTerms)
	}
	return size
}

// heldTermsSize returns what terms add to a held row's json.Marshal
// encoding.
func heldTermsSize(terms []TransferTerm) int64 {
	size, err := encodedSize(TransferredValue{HeldTerms: terms})
	if err != nil {
		return 0
	}
	return size - heldRowEncoding.base
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
