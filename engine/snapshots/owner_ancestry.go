package snapshots

import (
	"context"
	"crypto/rand"
	stderrors "errors"
	"strings"

	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/engine/wcprof"
	digest "github.com/opencontainers/go-digest"
	pkgerrors "github.com/pkg/errors"
)

// Owner leases are gc.flat: the collector keeps the snapshots a lease names
// and not their parents, so a snapshot's whole chain has to be named by some
// lease. A dagql owner lease names only its own snapshot (and that snapshot's
// blobs); the ancestors of every snapshot dagql owner leases own are named
// once, in one ancestry lease per manager, ownerAncestryLeasePrefix plus a
// random suffix, with reference counts kept in memory.
//
// Naming each snapshot's chain in each of its owner leases instead costs
// lease records proportional to owners times chain depth: one owner lease per
// result of a chain of N calls, each a layer on the last, is N²/2 records,
// all held in memory by the metadata store.
//
// Only dagql owner leases are managed so: they are removed only through
// RemoveLease, which releases their ancestors. Other leases that AttachLease
// fills (operation leases, pins, services) can be deleted directly from the
// lease manager, and name their whole chains themselves.
//
// A snapshot is kept while a dagql owner lease names it (tips) or a kept
// snapshot has it as parent (children): the ancestry lease names exactly the
// snapshots with children, and their blobs. An engine restart attaches the
// owner leases it keeps again, which rebuilds this state under a new ancestry
// lease; DeleteStaleDaggerOwnerLeases then deletes the previous one.
//
// The ancestry lease is under the dagql owner lease prefix, though it is no
// owner lease, so that an engine that predates it deletes it at boot, as a
// stale owner lease, after attaching the whole chains of the owners it keeps
// to those owners again.
const ownerAncestryLeasePrefix = dagqlResultLeasePrefix + "owner-ancestry/"

func ancestryManagedOwner(leaseID string) bool {
	return strings.HasPrefix(leaseID, dagqlResultLeasePrefix) && !strings.HasPrefix(leaseID, ownerAncestryLeasePrefix)
}

func newOwnerAncestryLeaseID() string {
	return ownerAncestryLeasePrefix + rand.Text()
}

// ownerAncestryLeaseID is the manager's ancestry lease ID, made on first use
// for managers built without NewSnapshotManager.
func (cm *snapshotManager) ownerAncestryLeaseID() string {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.ancestryLeaseID == "" {
		cm.ancestryLeaseID = newOwnerAncestryLeaseID()
	}
	return cm.ancestryLeaseID
}

type ownerAncestry struct {
	// tips counts the dagql owner leases recorded as owners of a snapshot.
	tips map[string]int
	// children counts a snapshot's kept children: the snapshots with it as
	// parent that are kept. The ancestry lease names a snapshot exactly
	// while it has children.
	children map[string]int
	// parent is each kept snapshot's parent, "" for none.
	parent map[string]string
	// blobs are the blobs the ancestry lease names for each snapshot it
	// names, and blobRefs counts the snapshots naming each blob.
	blobs    map[string]map[digest.Digest]struct{}
	blobRefs map[digest.Digest]int
}

func (a *ownerAncestry) initLocked() {
	if a.tips != nil {
		return
	}
	a.tips = map[string]int{}
	a.children = map[string]int{}
	a.parent = map[string]string{}
	a.blobs = map[string]map[digest.Digest]struct{}{}
	a.blobRefs = map[digest.Digest]int{}
}

func (a *ownerAncestry) kept(snapshotID string) bool {
	return a.tips[snapshotID] > 0 || a.children[snapshotID] > 0
}

// named reports whether the ancestry lease names snapshotID.
func (a *ownerAncestry) named(snapshotID string) bool {
	return a.children[snapshotID] > 0
}

// addBlobLocked records that the ancestry lease names dgst for snapshotID.
func (a *ownerAncestry) addBlobLocked(snapshotID string, dgst digest.Digest) {
	set := a.blobs[snapshotID]
	if set == nil {
		set = map[digest.Digest]struct{}{}
		a.blobs[snapshotID] = set
	}
	if _, ok := set[dgst]; ok {
		return
	}
	set[dgst] = struct{}{}
	a.blobRefs[dgst]++
}

// ancestryRelease is what the ancestry lease no longer needs to name.
type ancestryRelease struct {
	snapshots []string
	blobs     []digest.Digest
}

// releaseTipLocked drops one dagql owner of snapshotID, and when that leaves
// the snapshot unkept, releases it from its parent, and so on up the chain.
// What the ancestry lease no longer names is added to released.
func (a *ownerAncestry) releaseTipLocked(snapshotID string, released *ancestryRelease) {
	if a.tips[snapshotID] == 0 {
		return
	}
	a.tips[snapshotID]--
	if a.tips[snapshotID] == 0 {
		delete(a.tips, snapshotID)
	}
	for cur := snapshotID; cur != "" && !a.kept(cur); {
		parent := a.parent[cur]
		delete(a.parent, cur)
		if parent == "" {
			return
		}
		a.children[parent]--
		if a.children[parent] > 0 {
			return
		}
		delete(a.children, parent)
		released.snapshots = append(released.snapshots, parent)
		for dgst := range a.blobs[parent] {
			a.blobRefs[dgst]--
			if a.blobRefs[dgst] == 0 {
				delete(a.blobRefs, dgst)
				released.blobs = append(released.blobs, dgst)
			}
		}
		delete(a.blobs, parent)
		cur = parent
	}
}

// deleteAncestryResources takes released out of the ancestry lease.
func (cm *snapshotManager) deleteAncestryResources(ctx context.Context, released ancestryRelease) error {
	if len(released.snapshots) == 0 && len(released.blobs) == 0 {
		return nil
	}
	ctx = cm.withLeaseNamespace(ctx)
	lease := leases.Lease{ID: cm.ownerAncestryLeaseID()}
	return cm.updateMetadata(ctx, func(ctx context.Context) error {
		var rerr error
		for _, snapshotID := range released.snapshots {
			err := cm.LeaseManager.DeleteResource(ctx, lease, leases.Resource{ID: snapshotID, Type: "snapshots/" + cm.Snapshotter.Name()})
			if err != nil && !cerrdefs.IsNotFound(err) {
				rerr = stderrors.Join(rerr, pkgerrors.Wrapf(err, "release snapshot %s from owner ancestry lease", snapshotID))
			}
		}
		for _, dgst := range released.blobs {
			err := cm.LeaseManager.DeleteResource(ctx, lease, leases.Resource{ID: dgst.String(), Type: "content"})
			if err != nil && !cerrdefs.IsNotFound(err) {
				rerr = stderrors.Join(rerr, pkgerrors.Wrapf(err, "release content %s from owner ancestry lease", dgst))
			}
		}
		return rerr
	})
}

// attachOwnerLease is AttachLease for a dagql owner lease: it names
// snapshotID and its blobs, and the ancestry lease names its ancestors that
// no other kept snapshot has yet.
func (cm *snapshotManager) attachOwnerLease(ctx context.Context, leaseID, snapshotID string) error {
	cm.ownerLeaseLocker.Lock(leaseID)
	defer cm.ownerLeaseLocker.Unlock(leaseID)
	// ancestryMu serializes every change to the ancestry, from the walk
	// that reads it to the record of the change: a removal in between could
	// otherwise take an ancestor out of the ancestry lease that this attach
	// counted on.
	cm.ancestryMu.Lock()
	defer cm.ancestryMu.Unlock()
	ancestryLeaseID := cm.ownerAncestryLeaseID()

	cm.mu.Lock()
	cm.ancestry.initLocked()
	_, alreadyOwner := cm.snapshotOwnerLeases[snapshotID][leaseID]
	tipKept := cm.ancestry.kept(snapshotID)
	cm.mu.Unlock()

	// Walk up from the snapshot until the first kept ancestor, which keeps
	// the rest of the chain already. The ancestry is only read here, and
	// changes only under ancestryMu.
	labeledBlobs := map[string]digest.Digest{}
	walked := []string{} // the snapshot, then the ancestors that become kept
	parents := map[string]string{}
	missing := func(currentSnapshotID string, err error) error {
		if cerrdefs.IsNotFound(err) {
			err = pkgerrors.Wrap(errNotFound, currentSnapshotID)
		} else {
			err = pkgerrors.Wrapf(err, "stat snapshot %s for owner lease %s", currentSnapshotID, leaseID)
		}
		// The lease is created even when the walk fails, and a failure to
		// create it is the error, as when it was created before the walk.
		if _, createErr := cm.writeLease(ctx, func(ctx context.Context) error {
			return cm.createOwnerLease(ctx, leaseID)
		}); createErr != nil {
			return createErr
		}
		return err
	}
	for currentSnapshotID := snapshotID; currentSnapshotID != ""; {
		info, err := cm.Snapshotter.Stat(ctx, currentSnapshotID)
		if err != nil {
			return missing(currentSnapshotID, err)
		}
		walked = append(walked, currentSnapshotID)
		parents[currentSnapshotID] = info.Parent
		if blob := info.Labels[snapshotBlobGCLabel]; blob != "" {
			labeledBlobs[currentSnapshotID] = digest.Digest(blob)
		}
		if alreadyOwner || tipKept {
			// The snapshot is kept already, and so is its chain.
			break
		}
		currentSnapshotID = info.Parent
		if currentSnapshotID != "" && cm.ancestry.kept(currentSnapshotID) {
			break
		}
	}

	// The snapshots the ancestry lease starts to name: the walked
	// ancestors, and the kept ancestor the walk stopped at, if it has no
	// children yet (a snapshot kept only by owners of its own).
	var named []string
	stop := ""
	if !alreadyOwner && !tipKept {
		named = append(named, walked[1:]...)
		stop = parents[walked[len(walked)-1]]
		if stop != "" && !cm.ancestry.named(stop) {
			named = append(named, stop)
		}
	}

	blobs := cm.snapshotBlobs(ctx, append([]string{snapshotID}, named...), labeledBlobs)
	committed, err := cm.writeLease(ctx, func(ctx context.Context) error {
		if err := cm.createOwnerLease(ctx, leaseID); err != nil {
			return err
		}
		if err := cm.LeaseManager.AddResource(ctx, leases.Lease{ID: leaseID}, leases.Resource{
			ID:   snapshotID,
			Type: "snapshots/" + cm.Snapshotter.Name(),
		}); err != nil && !cerrdefs.IsAlreadyExists(err) {
			return pkgerrors.Wrapf(err, "attach snapshot %s to owner lease %s", snapshotID, leaseID)
		}
		for _, dgst := range blobs[snapshotID] {
			if err := cm.attachOwnerContent(ctx, leaseID, snapshotID, dgst); err != nil {
				return err
			}
		}
		if len(named) == 0 {
			return nil
		}
		if err := cm.createOwnerLease(ctx, ancestryLeaseID); err != nil {
			return err
		}
		for _, ancestor := range named {
			if err := cm.LeaseManager.AddResource(ctx, leases.Lease{ID: ancestryLeaseID}, leases.Resource{
				ID:   ancestor,
				Type: "snapshots/" + cm.Snapshotter.Name(),
			}); err != nil && !cerrdefs.IsAlreadyExists(err) {
				return pkgerrors.Wrapf(err, "attach snapshot %s to owner ancestry lease for %s", ancestor, leaseID)
			}
			for _, dgst := range blobs[ancestor] {
				if err := cm.attachOwnerContent(ctx, ancestryLeaseID, ancestor, dgst); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if !committed || err != nil {
		// Writes that committed before a failure name more than is
		// recorded, which keeps snapshots longer, never shorter.
		return err
	}

	// The lease becomes the snapshot's owner once the snapshot's blobs are
	// attached to it, as for any owner.
	if err := cm.recordOwner(ctx, leaseID, snapshotID, blobs[snapshotID]); err != nil {
		return err
	}
	if !alreadyOwner {
		cm.mu.Lock()
		cm.ancestry.tips[snapshotID]++
		if !tipKept {
			// Each walked snapshot became kept: record its parent, and
			// count it as a child of the parent.
			for _, walkedID := range walked {
				parent := parents[walkedID]
				cm.ancestry.parent[walkedID] = parent
				if parent != "" {
					cm.ancestry.children[parent]++
				}
			}
			for _, ancestor := range named {
				for _, dgst := range blobs[ancestor] {
					cm.ancestry.addBlobLocked(ancestor, dgst)
				}
			}
		}
		cm.mu.Unlock()
	}
	for _, ancestor := range named {
		if err := cm.attachPendingAncestryBlobs(ctx, ancestryLeaseID, ancestor); err != nil {
			return err
		}
	}
	return cm.validateAttached(ctx, leaseID, append([]string{snapshotID}, named...))
}

// recordAncestryContent attaches dgst, just recorded as snapshotID's blob,
// to the ancestry lease if it names the snapshot, and counts it there.
//
// It holds ancestryMu from the check to the count, as the attach and the
// removal that change the ancestry do: a removal that releases the snapshot
// in between would otherwise either have the blob added to the lease after
// the release, where nothing counts it and nothing deletes it, or, for a
// blob another released snapshot shares, delete it from the lease before it
// is counted for this snapshot, which stays kept with its blob unleased.
func (cm *snapshotManager) recordAncestryContent(ctx context.Context, snapshotID string, dgst digest.Digest) error {
	ancestryLeaseID := cm.ownerAncestryLeaseID()
	cm.ancestryMu.Lock()
	defer cm.ancestryMu.Unlock()
	wcprof.Lock(ctx, &cm.mu, managerMuProfIdent)
	_, counted := cm.ancestry.blobs[snapshotID][dgst]
	named := cm.ancestry.named(snapshotID)
	cm.mu.Unlock()
	if !named || counted {
		return nil
	}
	if err := cm.attachOwnerContent(context.WithoutCancel(ctx), ancestryLeaseID, snapshotID, dgst); err != nil {
		return err
	}
	wcprof.Lock(ctx, &cm.mu, managerMuProfIdent)
	cm.ancestry.addBlobLocked(snapshotID, dgst)
	cm.mu.Unlock()
	return nil
}

// attachPendingAncestryBlobs attaches to the ancestry lease the blobs
// recorded for snapshotID, which it names, after the attach read them: a
// blob recorded later is attached by recordSnapshotContent.
func (cm *snapshotManager) attachPendingAncestryBlobs(ctx context.Context, ancestryLeaseID, snapshotID string) error {
	for {
		cm.mu.Lock()
		var pending []digest.Digest
		if cm.ancestry.named(snapshotID) {
			for dgst := range cm.snapshotContentDigests[snapshotID] {
				if _, ok := cm.ancestry.blobs[snapshotID][dgst]; !ok {
					pending = append(pending, dgst)
				}
			}
		}
		cm.mu.Unlock()
		if len(pending) == 0 {
			return nil
		}
		committed, err := cm.writeLease(ctx, func(ctx context.Context) error {
			for _, dgst := range pending {
				if err := cm.attachOwnerContent(ctx, ancestryLeaseID, snapshotID, dgst); err != nil {
					return err
				}
			}
			return nil
		})
		if committed && err == nil {
			cm.mu.Lock()
			if cm.ancestry.named(snapshotID) {
				for _, dgst := range pending {
					cm.ancestry.addBlobLocked(snapshotID, dgst)
				}
			}
			cm.mu.Unlock()
		}
		if err != nil {
			return err
		}
	}
}
