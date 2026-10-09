package snapshots

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata/boltutil"
	cerrdefs "github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	pkgerrors "github.com/pkg/errors"
	bolt "go.etcd.io/bbolt"
)

const dagqlResultLeasePrefix = "dagql/result/"

// ContentHashMetadataKey stores the serialized per-path hash tree. Imported
// hashes must survive a restart: rescanning uses a different file hash format.
const ContentHashMetadataKey = "buildkit.contenthash.v0"

type PersistentMetadataRows struct {
	SnapshotContent []SnapshotContentRow
	ImportedByBlob  []ImportedLayerBlobRow
	ImportedByDiff  []ImportedLayerDiffRow
	ContentHashes   []SnapshotContentHashRow
}

type SnapshotContentHashRow struct {
	SnapshotID string
	Data       []byte
}

type SnapshotContentRow struct {
	SnapshotID string
	Digest     digest.Digest
}

type ImportedLayerBlobRow struct {
	ParentSnapshotID string
	BlobDigest       digest.Digest
	SnapshotID       string
}

type ImportedLayerDiffRow struct {
	ParentSnapshotID string
	DiffID           digest.Digest
	SnapshotID       string
}

type ImportedLayerBlobKey struct {
	ParentSnapshotID string
	BlobDigest       digest.Digest
}

type ImportedLayerDiffKey struct {
	ParentSnapshotID string
	DiffID           digest.Digest
}

func (cm *snapshotManager) LoadPersistentMetadata(rows PersistentMetadataRows) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.snapshotContentDigests = make(map[string]map[digest.Digest]struct{}, len(rows.SnapshotContent))
	for _, row := range rows.SnapshotContent {
		if row.SnapshotID == "" || row.Digest == "" {
			continue
		}
		if cm.snapshotContentDigests[row.SnapshotID] == nil {
			cm.snapshotContentDigests[row.SnapshotID] = make(map[digest.Digest]struct{})
		}
		cm.snapshotContentDigests[row.SnapshotID][row.Digest] = struct{}{}
	}

	cm.importedLayerByBlob = make(map[ImportedLayerBlobKey]string, len(rows.ImportedByBlob))
	for _, row := range rows.ImportedByBlob {
		if row.SnapshotID == "" || row.BlobDigest == "" {
			continue
		}
		cm.importedLayerByBlob[ImportedLayerBlobKey{
			ParentSnapshotID: row.ParentSnapshotID,
			BlobDigest:       row.BlobDigest,
		}] = row.SnapshotID
	}

	cm.importedLayerByDiff = make(map[ImportedLayerDiffKey]string, len(rows.ImportedByDiff))
	for _, row := range rows.ImportedByDiff {
		if row.SnapshotID == "" || row.DiffID == "" {
			continue
		}
		cm.importedLayerByDiff[ImportedLayerDiffKey{
			ParentSnapshotID: row.ParentSnapshotID,
			DiffID:           row.DiffID,
		}] = row.SnapshotID
	}

	if cm.snapshotOwnerLeases == nil {
		cm.snapshotOwnerLeases = make(map[string]map[string]struct{})
	}
	cm.metadataStore.mu.Lock()
	cm.metadataStore.contentHashes = make(map[string][]byte, len(rows.ContentHashes))
	for _, row := range rows.ContentHashes {
		if row.SnapshotID != "" && len(row.Data) != 0 {
			data := append([]byte(nil), row.Data...)
			if md, ok := cm.metadataStore.refs[row.SnapshotID]; ok {
				md.external[ContentHashMetadataKey] = data
			} else {
				// Rehydration still needs to discover the snapshot's other metadata.
				cm.metadataStore.contentHashes[row.SnapshotID] = data
			}
		}
	}
	cm.metadataStore.mu.Unlock()

	return nil
}

func (cm *snapshotManager) PersistentMetadataRows() PersistentMetadataRows {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	rows := PersistentMetadataRows{
		SnapshotContent: make([]SnapshotContentRow, 0, len(cm.snapshotContentDigests)),
		ImportedByBlob:  make([]ImportedLayerBlobRow, 0, len(cm.importedLayerByBlob)),
		ImportedByDiff:  make([]ImportedLayerDiffRow, 0, len(cm.importedLayerByDiff)),
	}

	for snapshotID, digests := range cm.snapshotContentDigests {
		for dgst := range digests {
			rows.SnapshotContent = append(rows.SnapshotContent, SnapshotContentRow{
				SnapshotID: snapshotID,
				Digest:     dgst,
			})
		}
	}

	for key, snapshotID := range cm.importedLayerByBlob {
		rows.ImportedByBlob = append(rows.ImportedByBlob, ImportedLayerBlobRow{
			ParentSnapshotID: key.ParentSnapshotID,
			BlobDigest:       key.BlobDigest,
			SnapshotID:       snapshotID,
		})
	}

	for key, snapshotID := range cm.importedLayerByDiff {
		rows.ImportedByDiff = append(rows.ImportedByDiff, ImportedLayerDiffRow{
			ParentSnapshotID: key.ParentSnapshotID,
			DiffID:           key.DiffID,
			SnapshotID:       snapshotID,
		})
	}

	// Keep hash records only for owned immutable snapshots. Mutable mirrors
	// and snapshots without owners do not belong in the durable checkpoint.
	cm.metadataStore.mu.RLock()
	defer cm.metadataStore.mu.RUnlock()
	for snapshotID, owners := range cm.snapshotOwnerLeases {
		if len(owners) == 0 {
			continue
		}
		data := cm.metadataStore.contentHashes[snapshotID]
		if md, ok := cm.metadataStore.refs[snapshotID]; ok {
			if rec, ok := cm.records[snapshotID]; ok && rec.mutable {
				continue
			}
			data = md.external[ContentHashMetadataKey]
		}
		if len(data) != 0 {
			rows.ContentHashes = append(rows.ContentHashes, SnapshotContentHashRow{
				SnapshotID: snapshotID,
				Data:       append([]byte(nil), data...),
			})
		}
	}

	return rows
}

func (cm *snapshotManager) AttachLease(ctx context.Context, leaseID, snapshotID string) error {
	if leaseID == "" {
		return stderrors.New("attach lease: empty lease ID")
	}
	if snapshotID == "" {
		return stderrors.New("attach lease: empty snapshot ID")
	}

	// ownerLeaseLocker serializes AttachLease and RemoveLease for one lease.
	// The containerd calls below run outside cm.mu: holding the manager lock
	// across them stalls every unrelated Get and GetBySnapshotID.
	cm.ownerLeaseLocker.Lock(leaseID)
	defer cm.ownerLeaseLocker.Unlock(leaseID)

	snapshotIDs := []string{}
	labeledBlobs := map[string]digest.Digest{}
	for currentSnapshotID := snapshotID; currentSnapshotID != ""; {
		info, err := cm.Snapshotter.Stat(ctx, currentSnapshotID)
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				err = pkgerrors.Wrap(errNotFound, currentSnapshotID)
			} else {
				err = pkgerrors.Wrapf(err, "stat snapshot %s for owner lease %s", currentSnapshotID, leaseID)
			}
			// The lease is created even when the walk fails, and a failure
			// to create it is the error, as when it was created before the
			// walk.
			if _, createErr := cm.writeLease(ctx, func(ctx context.Context) error {
				return cm.createOwnerLease(ctx, leaseID)
			}); createErr != nil {
				return createErr
			}
			return err
		}
		snapshotIDs = append(snapshotIDs, currentSnapshotID)
		if blob := info.Labels[snapshotBlobGCLabel]; blob != "" {
			labeledBlobs[currentSnapshotID] = digest.Digest(blob)
		}
		currentSnapshotID = info.Parent
	}

	// The lease, the chain and its blobs are written in one transaction. The
	// lease becomes an owner of a snapshot only after the transaction, once
	// the snapshot's blobs are attached, rechecking for blobs recorded
	// meanwhile; see addSnapshotOwner.
	blobs := cm.snapshotBlobs(snapshotIDs, labeledBlobs)
	attachedSnapshots := 0
	committed, err := cm.writeLease(ctx, func(ctx context.Context) error {
		if err := cm.createOwnerLease(ctx, leaseID); err != nil {
			return err
		}
		for _, currentSnapshotID := range snapshotIDs {
			err := cm.LeaseManager.AddResource(ctx, leases.Lease{ID: leaseID}, leases.Resource{
				ID:   currentSnapshotID,
				Type: "snapshots/" + cm.Snapshotter.Name(),
			})
			if err != nil && !cerrdefs.IsAlreadyExists(err) {
				return pkgerrors.Wrapf(err, "attach snapshot %s to owner lease %s", currentSnapshotID, leaseID)
			}
			for _, dgst := range blobs[currentSnapshotID] {
				if err := cm.attachOwnerContent(ctx, leaseID, currentSnapshotID, dgst); err != nil {
					return err
				}
			}
			attachedSnapshots++
		}
		return nil
	})
	if !committed {
		attachedSnapshots = 0
	}

	// Each snapshot whose writes committed gets its owner recorded, as when
	// each snapshot was attached and recorded in turn, even after a later
	// snapshot's write failed. A snapshot whose blobs recorded meanwhile fail
	// to attach is left without the owner; the first error is returned.
	for _, currentSnapshotID := range snapshotIDs[:attachedSnapshots] {
		attached := make(map[digest.Digest]struct{}, len(blobs[currentSnapshotID]))
		for _, dgst := range blobs[currentSnapshotID] {
			attached[dgst] = struct{}{}
		}
		for {
			pending := cm.addSnapshotOwner(currentSnapshotID, leaseID, attached)
			if len(pending) == 0 {
				break
			}
			var added []digest.Digest
			committed, attachErr := cm.writeLease(ctx, func(ctx context.Context) error {
				for _, dgst := range pending {
					if err := cm.attachOwnerContent(ctx, leaseID, currentSnapshotID, dgst); err != nil {
						return err
					}
					added = append(added, dgst)
				}
				return nil
			})
			if committed {
				for _, dgst := range added {
					attached[dgst] = struct{}{}
				}
			}
			if attachErr != nil {
				if err == nil {
					err = attachErr
				}
				break
			}
		}
	}
	if err != nil {
		return err
	}

	// AddResource does not check whether a target still exists. GC can finish
	// between the ancestry walk and attachment. Once attached, surviving
	// resources cannot be reclaimed by a later GC while this lease exists.
	for _, currentSnapshotID := range snapshotIDs {
		if _, err := cm.Snapshotter.Stat(ctx, currentSnapshotID); err != nil {
			if cerrdefs.IsNotFound(err) {
				return pkgerrors.Wrap(errNotFound, currentSnapshotID)
			}
			return pkgerrors.Wrapf(err, "validate snapshot %s for owner lease %s", currentSnapshotID, leaseID)
		}
	}

	return nil
}

// writeLease runs the lease writes fn makes in one metadata transaction,
// rather than one transaction per write, when the manager has the metadata
// database. The writes made before fn fails are committed, as separate writes
// would have been. committed reports whether they were: false only when the
// transaction itself failed and rolled back. fn must not take cm.mu: holders
// of cm.mu wait on metadata writes.
func (cm *snapshotManager) writeLease(ctx context.Context, fn func(context.Context) error) (committed bool, _ error) {
	if cm.metadataDB == nil {
		return true, fn(ctx)
	}
	var fnErr error
	err := cm.metadataDB.Update(func(tx *bolt.Tx) error {
		fnErr = fn(boltutil.WithTransaction(ctx, tx))
		return nil
	})
	if err != nil {
		return false, stderrors.Join(fnErr, pkgerrors.Wrap(err, "commit owner lease writes"))
	}
	return true, fnErr
}

// updateMetadata runs fn's metadata writes in one transaction, which is rolled
// back when fn fails. Without a metadata DB, fn's writes are made one by one.
// fn must not take cm.mu.
func (cm *snapshotManager) updateMetadata(ctx context.Context, fn func(context.Context) error) error {
	if cm.metadataDB == nil {
		return fn(ctx)
	}
	return cm.metadataDB.Update(func(tx *bolt.Tx) error {
		return fn(boltutil.WithTransaction(ctx, tx))
	})
}

func (cm *snapshotManager) createOwnerLease(ctx context.Context, leaseID string) error {
	_, err := cm.LeaseManager.Create(ctx, func(l *leases.Lease) error {
		l.ID = leaseID
		l.Labels = map[string]string{
			"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
		}
		return nil
	})
	if err != nil && !cerrdefs.IsAlreadyExists(err) {
		return pkgerrors.Wrapf(err, "create owner lease %s", leaseID)
	}
	return nil
}

func (cm *snapshotManager) attachOwnerContent(ctx context.Context, leaseID, snapshotID string, dgst digest.Digest) error {
	err := cm.LeaseManager.AddResource(ctx, leases.Lease{ID: leaseID}, leases.Resource{
		ID:   dgst.String(),
		Type: "content",
	})
	if err != nil && !cerrdefs.IsAlreadyExists(err) {
		return pkgerrors.Wrapf(err, "attach content %s for snapshot %s to owner lease %s", dgst, snapshotID, leaseID)
	}
	return nil
}

// snapshotBlobs returns each snapshot's blobs: those recorded in this
// process, and the one its label names, which survives a restart. The lease
// is flat, so the collector will not follow the label itself; the resource is
// what keeps the blob.
func (cm *snapshotManager) snapshotBlobs(snapshotIDs []string, labeledBlobs map[string]digest.Digest) map[string][]digest.Digest {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	blobs := make(map[string][]digest.Digest, len(snapshotIDs))
	for _, snapshotID := range snapshotIDs {
		if dgst, ok := labeledBlobs[snapshotID]; ok {
			// Recorded again in memory, so SnapshotSize sees the blob after a
			// restart as it does within the process that wrote it.
			if cm.snapshotContentDigests[snapshotID] == nil {
				cm.snapshotContentDigests[snapshotID] = make(map[digest.Digest]struct{})
			}
			cm.snapshotContentDigests[snapshotID][dgst] = struct{}{}
		}
		for dgst := range cm.snapshotContentDigests[snapshotID] {
			blobs[snapshotID] = append(blobs[snapshotID], dgst)
		}
	}
	return blobs
}

// addSnapshotOwner records leaseID as an owner of snapshotID once every blob
// recorded for the snapshot is in attached, and otherwise returns the blobs
// still to attach.
//
// The check and the record happen under one hold of cm.mu: a blob that
// recordSnapshotContent records concurrently is either returned here, or
// recorded after the owner and attached to it by recordSnapshotContent. As
// when all of AttachLease ran under cm.mu, a lease becomes an owner only once
// the snapshot's blobs are attached to it.
func (cm *snapshotManager) addSnapshotOwner(snapshotID, leaseID string, attached map[digest.Digest]struct{}) []digest.Digest {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	var pending []digest.Digest
	for dgst := range cm.snapshotContentDigests[snapshotID] {
		if _, ok := attached[dgst]; !ok {
			pending = append(pending, dgst)
		}
	}
	if len(pending) > 0 {
		return pending
	}

	if cm.snapshotOwnerLeases[snapshotID] == nil {
		cm.snapshotOwnerLeases[snapshotID] = make(map[string]struct{})
	}
	cm.snapshotOwnerLeases[snapshotID][leaseID] = struct{}{}
	return nil
}

func (cm *snapshotManager) RemoveLease(ctx context.Context, leaseID string) error {
	if leaseID == "" {
		return nil
	}

	cm.ownerLeaseLocker.Lock(leaseID)
	defer cm.ownerLeaseLocker.Unlock(leaseID)

	err := cm.LeaseManager.Delete(ctx, leases.Lease{ID: leaseID})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return pkgerrors.Wrapf(err, "delete owner lease %s", leaseID)
	}

	cm.mu.Lock()
	for snapshotID, leaseIDs := range cm.snapshotOwnerLeases {
		delete(leaseIDs, leaseID)
		if len(leaseIDs) == 0 {
			delete(cm.snapshotOwnerLeases, snapshotID)
		}
	}
	cm.mu.Unlock()

	return nil
}

func (cm *snapshotManager) DeleteStaleDaggerOwnerLeases(ctx context.Context, keep map[string]struct{}) error {
	leasesList, err := cm.LeaseManager.List(ctx)
	if err != nil {
		return pkgerrors.Wrap(err, "list leases")
	}

	var rerr error
	for _, lease := range leasesList {
		if !strings.HasPrefix(lease.ID, dagqlResultLeasePrefix) {
			continue
		}
		if _, ok := keep[lease.ID]; ok {
			continue
		}
		rerr = stderrors.Join(rerr, cm.RemoveLease(ctx, lease.ID))
	}
	return rerr
}
