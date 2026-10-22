package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/opencontainers/go-digest"
)

// StoredSize is what a root adds to a stored bundle: its values, and the sum
// of their encoded sizes, each as json.Marshal encodes a TransferredValue.
type StoredSize struct {
	Records int
	Bytes   int64
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
// and its size memoized with its normalized record (storedTransferRecordLocked)
// until the record, its dependencies, replacement count or expiry change. A
// held row's recipe is measured by one of the entry's recipe keys. A bundle
// numbers references by ordinal
// instead, so a size differs from the bundle's encoding by the digits of
// each reference. It takes egraphMu.RLock for each root in turn, not across
// all of them. Only a blob-backed cache answers.
func (c *Cache) StoredSizes(ctx context.Context, roots []uint64, held *HeldBy) ([]StoredSize, error) {
	if !c.blobBacked {
		return nil, fmt.Errorf("stored sizes: the cache has no blob store")
	}
	sizes := make([]StoredSize, len(roots))
	seen := map[sharedResultID]bool{}
	for i, number := range roots {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		size, err := c.storedRootSize(sharedResultID(number), held, seen)
		if err != nil {
			return nil, err
		}
		sizes[i] = size
	}
	return sizes, nil
}

// storedRootSize measures what root's closure adds beyond the entries in
// seen, and adds them to it, in one hold of egraphMu.RLock.
func (c *Cache) storedRootSize(root sharedResultID, held *HeldBy, seen map[sharedResultID]bool) (StoredSize, error) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	var size StoredSize
	res := c.resultsByID[root]
	if res == nil || res.noValueLocked() {
		return size, nil
	}
	stack := []*sharedResult{res}
	for len(stack) > 0 {
		res := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[res.id] {
			continue
		}
		seen[res.id] = true
		if held.heldLocked(res) {
			bytes, err := heldRowSizeLocked(res)
			if err != nil {
				return StoredSize{}, err
			}
			size.Records++
			size.Bytes += bytes
			continue
		}
		if res.noValueLocked() || res.persistedEnvelope == nil {
			continue
		}
		bytes, err := storedValueSizeLocked(res)
		if err != nil {
			return StoredSize{}, fmt.Errorf("stored sizes: entry %d: %w", res.id, err)
		}
		size.Records++
		size.Bytes += bytes
		for _, id := range storedClosureChildrenLocked(res) {
			if child := c.resultsByID[id]; child != nil && !seen[id] {
				stack = append(stack, child)
			}
		}
	}
	return size, nil
}

// storedValueSizeLocked returns the encoded size of res's value as a stored
// bundle carries it, with references at entry numbers, measuring it again
// once its record, dependencies, replacement count or expiry change.
// Requires egraphMu.
func storedValueSizeLocked(res *sharedResult) (int64, error) {
	rec, err := storedTransferRecordLocked(res)
	if err != nil {
		return 0, err
	}
	kept := res.storedTransfer.Load()
	if size := kept.size.Load(); size != nil && size.dependencyRevision == res.dependencyOwnershipRevision && size.replacements == res.replacements && size.expiresAtUnix == res.expiresAtUnix {
		return size.bytes, nil
	}
	value := TransferredValue{Ordinal: TransferOrdinal(res.id), SenderNumber: uint64(res.id), SenderReplacements: res.replacements, Record: rec, ExpiresAtUnix: res.expiresAtUnix}
	for dep := range res.deps {
		value.DependencyIDs = append(value.DependencyIDs, uint64(dep))
	}
	slices.Sort(value.DependencyIDs)
	data, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	kept.size.Store(&storedValueSize{dependencyRevision: res.dependencyOwnershipRevision, replacements: res.replacements, expiresAtUnix: res.expiresAtUnix, bytes: int64(len(data))})
	return int64(len(data)), nil
}

// heldRowSizeLocked returns the encoded size of res as a held row, its
// recipe measured by one of its recipe keys: StoredBundle derives the recipe
// from its call frame after the lock, and recipes are of one length.
// Requires egraphMu.
func heldRowSizeLocked(res *sharedResult) (int64, error) {
	recipe := heldRowSizeDigest
	if len(res.recipeKeys) > 0 {
		recipe = res.recipeKeys[0]
	}
	data, err := json.Marshal(TransferredValue{Ordinal: TransferOrdinal(res.id), SenderNumber: uint64(res.id), SenderReplacements: res.replacements, Record: PersistedRecord{ResultID: uint64(res.id)}, ExpiresAtUnix: res.expiresAtUnix, Held: recipe})
	if err != nil {
		return 0, err
	}
	return int64(len(data)), nil
}
