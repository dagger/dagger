package dagql

import (
	"context"
	"fmt"
	"slices"

	"github.com/opencontainers/go-digest"
)

// RemoteWrites is a Cloud cache held for writing (HoldRemoteWrites), for a
// run of operations on its holdings applied in one hold of its lock. Each
// method is the Cache's method of the same name, with the same rules and the
// same change: a run of them changes the cache as the Cache's methods called
// one by one in the same order do, and each returns what it changed. What
// the run saves is the waits: a reader of the cache, such as a bundle's
// build, holds its lock for as long as it reads, and a writer waiting for the
// lock stops new readers until it has had it, so each of the Cache's methods
// waits for every read already in progress, and the run waits for them once.
//
// The holder calls nothing else of the cache until Release: the cache's own
// methods wait for the hold. Only the Cloud uses it, to apply a read of its
// engines' telemetry and a step's merged replies.
type RemoteWrites struct {
	c *Cache
	// releases are what the hold's collections released, run once the hold
	// ends, as CollectRemoteHoldings runs them once its own hold ends.
	releases []OnReleaseFunc
}

// HoldRemoteWrites takes the cache's lock for writing, until the returned
// hold's Release.
func (c *Cache) HoldRemoteWrites() *RemoteWrites {
	c.egraphMu.Lock()
	return &RemoteWrites{c: c}
}

// Contended reports whether anything waits for the cache's lock now, to
// read the cache or to write it. A holder applying a run of writes asks it
// between two of them, and lets the hold go and takes it again only when
// something waits: a reader then waits for the run's write in progress, not
// for the whole run, and the run waits for the cache's readers again only
// when it kept one out.
func (w *RemoteWrites) Contended() bool {
	return w.c.egraphMu.Waiting()
}

// Release ends the hold, and then runs what its collections released,
// returning their error. The hold can't be used after.
func (w *RemoteWrites) Release(ctx context.Context) error {
	c, releases := w.c, w.releases
	w.c, w.releases = nil, nil
	c.egraphMu.Unlock()
	return runOnReleaseFuncs(ctx, releases)
}

// AttachRemoteHolding is Cache.AttachRemoteHolding, in the hold.
func (w *RemoteWrites) AttachRemoteHolding(ctx context.Context, key HolderKey, desc RemoteHolding) (RemoteChange, error) {
	parts, kept, err := prepareRemoteHolding(key, desc)
	if err != nil {
		return RemoteChange{}, err
	}
	return w.c.attachRemoteHoldingLocked(ctx, key, desc, parts, kept), nil
}

// UpdateRemoteHolding is Cache.UpdateRemoteHolding, in the hold.
func (w *RemoteWrites) UpdateRemoteHolding(ctx context.Context, key HolderKey, update RemoteHoldingUpdate) (RemoteChange, bool, error) {
	parts, err := prepareRemoteHoldingUpdate(key, update)
	if err != nil {
		return RemoteChange{}, false, err
	}
	change, found := w.c.updateRemoteHoldingLocked(ctx, key, update, parts)
	return change, found, nil
}

// AddRemoteHold is Cache.AddRemoteHold, in the hold.
func (w *RemoteWrites) AddRemoteHold(ctx context.Context, key HolderKey, session string) (RemoteChange, error) {
	if session == "" {
		return RemoteChange{}, errEmptyRemoteHoldSession
	}
	return w.c.addRemoteHoldLocked(key, session)
}

// ObserveRemoteRetention is Cache.ObserveRemoteRetention, in the hold.
func (w *RemoteWrites) ObserveRemoteRetention(ctx context.Context, key HolderKey, obs RetentionObservation) (RemoteChange, error) {
	return w.c.observeRemoteRetentionLocked(key, obs)
}

// ReleaseRemoteSession is Cache.ReleaseRemoteSession, in the hold.
func (w *RemoteWrites) ReleaseRemoteSession(ctx context.Context, cache CacheID, session string) RemoteChange {
	return w.c.releaseRemoteSessionChangeLocked(cache, session)
}

// ReleaseRemoteCache is Cache.ReleaseRemoteCache, in the hold.
func (w *RemoteWrites) ReleaseRemoteCache(ctx context.Context, cache CacheID) RemoteChange {
	return w.c.releaseRemoteCacheLocked(cache)
}

// CollectRemoteHoldings is Cache.CollectRemoteHoldings, in the hold. What
// the collected entries release runs at Release, whose error reports it.
func (w *RemoteWrites) CollectRemoteHoldings(ctx context.Context, candidates []HolderKey, owned func(HolderKey) bool) ([]HolderKey, RemoteChange, error) {
	collected, change, releases, err := w.c.collectRemoteHoldingsLocked(ctx, candidates, owned)
	w.releases = append(w.releases, releases...)
	slices.SortFunc(collected, compareHolderKeys)
	return collected, change, err
}

// ApplyMergedReply is Cache.ApplyMergedReply, in the hold.
func (w *RemoteWrites) ApplyMergedReply(ctx context.Context, cache CacheID, sent ValueBundle, reply MergeReply) (RemoteChange, error) {
	cloudEntries, targets, err := prepareMergedReply(cache, sent, reply)
	if err != nil {
		return RemoteChange{}, err
	}
	return w.c.applyMergedReplyLocked(ctx, cache, cloudEntries, targets, reply), nil
}

// ApplyMergedReplyPiece applies the next records of a merged reply
// prepared to apply in pieces, in the hold: one at least, and then one at a
// time until stop, asked between two records, reports true or the records
// run out. With its last records it applies the reply's roots' retention.
// It returns what it changed and whether the reply is applied whole. A hold
// let go between pieces lets the cache's readers in (MergedReplyApply): a
// holder that stops when someone waits (Contended) keeps a reader waiting
// for one record, not the reply.
func (w *RemoteWrites) ApplyMergedReplyPiece(ctx context.Context, a *MergedReplyApply, stop func() bool) (change RemoteChange, done bool) {
	defer w.c.takeChangesLocked(&change)
	for a.next < len(a.targets) {
		w.c.applyMergedTargetsLocked(ctx, a.cache, a.cloudEntries, a.targets[a.next:a.next+1], &change)
		a.next++
		if a.next < len(a.targets) && stop() {
			return change, false
		}
	}
	w.c.applyMergedRootsLocked(a.cache, a.reply, &change)
	return change, true
}

// RemoteHoldingInfo is Cache.RemoteHoldingInfo, in the hold.
func (w *RemoteWrites) RemoteHoldingInfo(key HolderKey) (RemoteHeldEntry, bool) {
	return w.c.remoteHoldingInfoLocked(key)
}

// HolderEntry is Cache.HolderEntry, in the hold.
func (w *RemoteWrites) HolderEntry(key HolderKey) (uint64, bool) {
	return w.c.holderEntryLocked(key)
}

// ApplyHeldValueState is Cache.ApplyHeldValueState, in the hold.
func (w *RemoteWrites) ApplyHeldValueState(ctx context.Context, key HolderKey, obs HeldValueState) (RemoteChange, bool, error) {
	parts, err := heldPartKeys(obs.Parts)
	if err != nil {
		return RemoteChange{}, false, fmt.Errorf("apply held value state %s/%d: %w", key.Cache, key.Number, err)
	}
	offered, err := heldPartKeys(obs.OfferedParts)
	if err != nil {
		return RemoteChange{}, false, fmt.Errorf("apply held value state %s/%d: %w", key.Cache, key.Number, err)
	}
	change, found := w.c.applyHeldValueStateOfLocked(ctx, key, obs, parts, offered)
	return change, found, nil
}

// EntryRecipe is Cache.EntryRecipe, in the hold.
func (w *RemoteWrites) EntryRecipe(number uint64) (digest.Digest, bool) {
	return w.c.entryRecipeLocked(number)
}

// HolderClosure is Cache.HolderClosure, in the hold.
func (w *RemoteWrites) HolderClosure(key HolderKey) []HolderKey {
	return w.c.holderClosureLocked(key)
}

// HolderGraph is Cache.HolderGraph, in the hold.
func (w *RemoteWrites) HolderGraph(cache CacheID, roots []uint64) map[uint64][]uint64 {
	graph, _ := w.c.holderGraphLocked(cache, roots, nil)
	return graph
}

// EntryClassEntries is Cache.EntryClassEntries, in the hold.
func (w *RemoteWrites) EntryClassEntries(number uint64) (EntryClass, bool) {
	return w.c.entryClassEntriesLocked(number)
}
