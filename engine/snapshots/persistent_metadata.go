package snapshots

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	pkgerrors "github.com/pkg/errors"
)

const dagqlResultLeasePrefix = "dagql/result/"

type PersistentMetadataRows struct {
	SnapshotContent []SnapshotContentRow
	ImportedByBlob  []ImportedLayerBlobRow
	ImportedByDiff  []ImportedLayerDiffRow
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

	return rows
}

func (cm *snapshotManager) AttachLease(ctx context.Context, leaseID, snapshotID string) error {
	if leaseID == "" {
		return stderrors.New("attach lease: empty lease ID")
	}
	if snapshotID == "" {
		return stderrors.New("attach lease: empty snapshot ID")
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

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

	_, err = cm.Snapshotter.Stat(ctx, snapshotID)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return pkgerrors.Wrap(errNotFound, snapshotID)
		}
		return pkgerrors.Wrapf(err, "stat snapshot %s for owner lease %s", snapshotID, leaseID)
	}

	err = cm.LeaseManager.AddResource(ctx, leases.Lease{ID: leaseID}, leases.Resource{
		ID:   snapshotID,
		Type: "snapshots/" + cm.Snapshotter.Name(),
	})
	if err != nil && !cerrdefs.IsAlreadyExists(err) {
		return pkgerrors.Wrapf(err, "attach snapshot %s to owner lease %s", snapshotID, leaseID)
	}

	for dgst := range cm.snapshotContentDigests[snapshotID] {
		err = cm.LeaseManager.AddResource(ctx, leases.Lease{ID: leaseID}, leases.Resource{
			ID:   dgst.String(),
			Type: "content",
		})
		if err != nil && !cerrdefs.IsAlreadyExists(err) {
			return pkgerrors.Wrapf(err, "attach content %s to owner lease %s", dgst, leaseID)
		}
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

	err := cm.LeaseManager.Delete(ctx, leases.Lease{ID: leaseID})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return pkgerrors.Wrapf(err, "delete owner lease %s", leaseID)
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()
	for snapshotID, leaseIDs := range cm.snapshotOwnerLeases {
		delete(leaseIDs, leaseID)
		if len(leaseIDs) == 0 {
			delete(cm.snapshotOwnerLeases, snapshotID)
		}
	}

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
