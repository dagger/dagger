package snapshots

import (
	"context"
	cerrdefs "github.com/containerd/errdefs"
	"slices"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func (sr *immutableRef) ExportChain(ctx context.Context, refCfg config.RefConfig) (_ *ExportChain, rerr error) {
	if sr == nil {
		return &ExportChain{}, nil
	}

	pin, ctx, err := sr.cm.newResourcePin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil {
			_ = pin.release(ctx)
		}
	}()
	if err := sr.cm.AttachLease(ctx, pin.id, sr.SnapshotID()); err != nil {
		return nil, err
	}

	snapshotIDs := []string{}
	for snapshotID := sr.SnapshotID(); snapshotID != ""; {
		snapshotIDs = append(snapshotIDs, snapshotID)
		info, err := sr.cm.Snapshotter.Stat(ctx, snapshotID)
		if err != nil {
			return nil, err
		}
		snapshotID = info.Parent
	}
	slices.Reverse(snapshotIDs)

	chain := &ExportChain{
		Layers:   make([]ExportLayer, 0, len(snapshotIDs)),
		Provider: sr.cm.ContentStore,
		pin:      pin,
	}

	var parentSnapshotID, reuseParentID string
	for _, snapshotID := range snapshotIDs {
		if parentSnapshotID == "" && isScratchSnapshotID(snapshotID) {
			parentSnapshotID = snapshotID
			continue
		}
		opened, err := sr.cm.GetBySnapshotID(ctx, snapshotID, NoUpdateLastUsed)
		if err != nil {
			return nil, err
		}
		layer := opened.(*immutableRef)

		desc, hasLayer, err := sr.cm.ensureExportBlob(ctx, parentSnapshotID, layer, refCfg.Compression)
		description := layer.GetDescription()
		createdAt := layer.GetCreatedAt()
		if releaseErr := layer.Release(context.WithoutCancel(ctx)); releaseErr != nil && err == nil {
			err = releaseErr
		}
		if err != nil {
			return nil, err
		}

		if hasLayer {
			desc = exportDescriptor(desc, refCfg.PreferNonDistributable)
			present, err := sr.cm.pinContent(ctx, desc)
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, errors.Wrapf(cerrdefs.ErrNotFound, "exported content %s disappeared", desc.Digest)
			}
			if err := sr.cm.registerExportLayer(reuseParentID, snapshotID, desc); err != nil {
				return nil, err
			}
			reuseParentID = snapshotID
			chain.Layers = append(chain.Layers, ExportLayer{
				Descriptor:  desc,
				Description: description,
				CreatedAt:   &createdAt,
			})
		}
		// Only a root actually omitted from the chain has the empty reuse
		// parent key. Keep its real ID for diffing the following snapshot.
		parentSnapshotID = snapshotID
	}

	return chain, nil
}

func exportDescriptor(desc ocispecs.Descriptor, preferNonDist bool) ocispecs.Descriptor {
	if preferNonDist && len(desc.URLs) > 0 {
		desc.MediaType = layerToNonDistributable(desc.MediaType)
		return desc
	}
	if len(desc.URLs) == 0 {
		desc.MediaType = layerToDistributable(desc.MediaType)
	}
	return desc
}

func getBlobWithCompressionWithRetry(ctx context.Context, ref *immutableRef, comp compression.Config) (ocispecs.Descriptor, error) {
	if blobDesc, err := ref.getBlobWithCompression(ctx, comp.Type); err == nil {
		present, err := ref.cm.pinContent(ctx, blobDesc)
		if err != nil {
			return ocispecs.Descriptor{}, err
		}
		if present {
			return blobDesc, nil
		}
	} else if !cerrdefs.IsNotFound(err) {
		return ocispecs.Descriptor{}, err
	}
	if err := ensureCompression(ctx, ref, comp); err != nil {
		return ocispecs.Descriptor{}, errors.Wrapf(err, "failed to ensure compression type %q", comp.Type)
	}
	return ref.getBlobWithCompression(ctx, comp.Type)
}

func (cm *snapshotManager) registerExportLayer(parentSnapshotID, snapshotID string, desc ocispecs.Descriptor) error {
	diffID, err := diffIDFromDescriptor(desc)
	if err != nil {
		return err
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.importedLayerByBlob[ImportedLayerBlobKey{ParentSnapshotID: parentSnapshotID, BlobDigest: desc.Digest}] = snapshotID
	cm.importedLayerByDiff[ImportedLayerDiffKey{ParentSnapshotID: parentSnapshotID, DiffID: diffID}] = snapshotID
	return nil
}
