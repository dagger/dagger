package snapshots

import (
	"context"
	"slices"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func (sr *immutableRef) ExportChain(ctx context.Context, refCfg config.RefConfig) (*ExportChain, error) {
	if sr == nil {
		return &ExportChain{}, nil
	}

	if _, ok := leases.FromContext(ctx); !ok {
		leaseCtx, done, err := leaseutil.WithLease(ctx, sr.cm.LeaseManager, leaseutil.MakeTemporary)
		if err != nil {
			return nil, err
		}
		defer done(context.WithoutCancel(leaseCtx))
		ctx = leaseCtx
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
	}

	var parentSnapshotID string
	for _, snapshotID := range snapshotIDs {
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
			chain.Layers = append(chain.Layers, ExportLayer{
				Descriptor:  desc,
				Description: description,
				CreatedAt:   &createdAt,
			})
		}
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
		return blobDesc, nil
	}
	if err := ensureCompression(ctx, ref, comp); err != nil {
		return ocispecs.Descriptor{}, errors.Wrapf(err, "failed to get and ensure compression type of %q", comp.Type)
	}
	return ref.getBlobWithCompression(ctx, comp.Type)
}
