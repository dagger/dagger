package dagql

import (
	"context"
	"fmt"
	"slices"
)

// A part a demand found complete and opened stays usable for as long as
// nothing about its row changes, so a later evaluation of it needs no
// acquisition: no task, no probe and no reopening, as a local result needs
// no evaluation once it has been evaluated. Each use still checks the
// session's resource requirements, which are access control.

// partReuse is the state of a row a part was found complete and opened at.
// A gate change (an installation, a permit, a settlement), an offer's
// attachment, replacement or retirement, a dependency change (whose
// snapshots a demand's owner lease sync would cover), a value replacement or
// a payload change (a stored-part prune) each move one of them, and a reuse
// then demands the part again.
type partReuse struct {
	gateRevision       uint64
	transferRevision   uint64
	dependencyRevision uint64
	replacements       uint64
	payloadRevision    uint64
	// parts are a whole scope's part keys, for a scope's record.
	parts []string
}

// currentPartReuseLocked is row's state now. egraphMu and gate.mu are held;
// payloadMu is taken under them.
func currentPartReuseLocked(row *sharedResult, gate *PartWriterGate) partReuse {
	return partReuse{
		gateRevision:       gate.revision,
		transferRevision:   row.transferRevision,
		dependencyRevision: row.dependencyOwnershipRevision,
		replacements:       row.replacements,
		payloadRevision:    row.loadPayloadState().payloadRevision,
	}
}

// sameState reports whether a and b are the same state of a row.
func (a partReuse) sameState(b partReuse) bool {
	return a.gateRevision == b.gateRevision && a.transferRevision == b.transferRevision &&
		a.dependencyRevision == b.dependencyRevision && a.replacements == b.replacements &&
		a.payloadRevision == b.payloadRevision
}

// partScopeReuseKey is the record key of a whole output scope, whose parts
// the scope's metadata names.
func partScopeReuseKey(path PersistedRefPath) string {
	return fmt.Sprintf("scope:%v", path)
}

// capturePartReuse returns row's state as a demand sees it before it probes
// a part, for recordPartReuse to compare against once the part is opened.
func (c *Cache) capturePartReuse(row *sharedResult) partReuse {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	gate := row.partGate.loadOrCreate()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return currentPartReuseLocked(row, gate)
}

// recordPartReuse records key's part (or scope, with its parts) of row as
// complete and opened, as seen when it was probed, unless row has changed
// since: a record must never date a reopening it didn't see.
func (c *Cache) recordPartReuse(row *sharedResult, key string, seen partReuse) bool {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	if c.resultsByID[row.id] != row {
		return false
	}
	gate := row.partGate.loadOrCreate()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if !seen.sameState(currentPartReuseLocked(row, gate)) {
		return false
	}
	if gate.reusable == nil {
		gate.reusable = make(map[string]partReuse)
	}
	gate.reusable[key] = seen
	return true
}

// recordAcquiredPart records address's part of res as reusable after a demand
// opened it at seen. A demand's own task moves the row's state when it
// installs the part (its settlement) or syncs the row's owner leases over a
// new snapshot, so when the state has moved, the part is checked again as
// the finished task left it, without another task: probed complete and not
// being written, and opened.
func (c *Cache) recordAcquiredPart(ctx context.Context, res AnyResult, address PersistedPartAddress, seen partReuse) {
	row := res.cacheSharedResult()
	key, err := partAddressKey(address)
	if err != nil || c.recordPartReuse(row, key, seen) {
		return
	}
	seen = c.capturePartReuse(row)
	_, _, probe, err := c.probePart(ctx, row, address)
	if err != nil || probe == nil || !probe.LocalComplete || probe.Busy {
		return
	}
	if c.openAcquiredPart(ctx, res, address) != nil {
		return
	}
	c.recordPartReuse(row, key, seen)
}

// partReusableLocked reports whether every part of keys was recorded complete
// and opened at row's current state, and none of them is being written now.
// egraphMu is held, for reading or writing; gate.mu is taken under it.
func partReusableLocked(row *sharedResult, keys []string) bool {
	gate := row.partGate.gate.Load()
	if gate == nil || len(keys) == 0 {
		return false
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	now := currentPartReuseLocked(row, gate)
	current := func(key string) (partReuse, bool) {
		reuse, ok := gate.reusable[key]
		return reuse, ok && reuse.sameState(now)
	}
	for _, group := range gate.groups {
		if group.phase == LazyEvaluationRunning {
			return false
		}
	}
	var partKeys []string
	for _, key := range keys {
		reuse, ok := current(key)
		if !ok {
			return false
		}
		if reuse.parts != nil {
			partKeys = append(partKeys, reuse.parts...)
		} else {
			partKeys = append(partKeys, key)
		}
	}
	for _, key := range partKeys {
		if _, ok := current(key); !ok {
			return false
		}
		if gate.outputs[key].phase == PartOutputInstalled {
			return false
		}
		for _, writer := range gate.writers {
			if writerKey, err := partAddressKey(writer.address); err == nil && writerKey == key {
				return false
			}
		}
	}
	return true
}

// reuseAcquiredScope answers an evaluation of path's parts (all of them when
// parts is empty) of row without acquiring them, when they were all found
// complete and opened at row's current state and the requesting session
// satisfies row's resource requirements. It reports whether it answered.
func (c *Cache) reuseAcquiredScope(ctx context.Context, row *sharedResult, path PersistedRefPath, parts []PartKey) bool {
	keys := []string{partScopeReuseKey(path)}
	if len(parts) > 0 {
		keys = keys[:0]
		for _, part := range parts {
			key, err := partAddressKey(PersistedPartAddress{OutputPath: path, Part: part})
			if err != nil {
				return false
			}
			keys = append(keys, key)
		}
	}
	session, err := partSession(ctx)
	if err != nil {
		return false
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.resultsByID[row.id] == row &&
		partReusableLocked(row, keys) &&
		c.sessionSatisfiesResourceRequirementsLocked(session, row)
}

// recordAcquiredScope records a whole scope of row, whose parts are parts, as
// reusable, as seen before its metadata was probed. When the scope's own
// demands moved the row's state, its metadata is read again as they left it,
// and the scope is recorded if it still names the same parts.
func (c *Cache) recordAcquiredScope(ctx context.Context, row *sharedResult, path PersistedRefPath, parts []PartKey, seen partReuse) {
	keys, ok := scopePartKeys(path, parts)
	if !ok {
		return
	}
	seen.parts = keys
	if c.recordPartReuse(row, partScopeReuseKey(path), seen) {
		return
	}
	now := c.capturePartReuse(row)
	record, _, _, err := c.probePart(ctx, row, PersistedPartAddress{OutputPath: path, Part: "metadata"})
	if err != nil {
		return
	}
	outputs, err := mapTransferredOutputs(record)
	if err != nil {
		return
	}
	var current []PartKey
	for _, out := range outputs {
		if slices.Equal(out.Address.OutputPath, path) {
			current = append(current, out.Address.Part)
		}
	}
	if currentKeys, ok := scopePartKeys(path, current); !ok || !slices.Equal(currentKeys, keys) {
		return
	}
	now.parts = keys
	c.recordPartReuse(row, partScopeReuseKey(path), now)
}

// scopePartKeys is parts' address keys under path, sorted.
func scopePartKeys(path PersistedRefPath, parts []PartKey) ([]string, bool) {
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		key, err := partAddressKey(PersistedPartAddress{OutputPath: path, Part: part})
		if err != nil {
			return nil, false
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys, true
}
