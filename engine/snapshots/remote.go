package snapshots

import (
	"context"
	stderrors "errors"
	"slices"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

func (sr *immutableRef) ExportChain(ctx context.Context, refCfg config.RefConfig) (*ExportChain, error) {
	if sr == nil {
		return &ExportChain{}, nil
	}
	chains, err := sr.cm.exportChains(ctx, []*immutableRef{sr}, nil, refCfg)
	if err != nil {
		return nil, err
	}
	return chains[0], nil
}

// ExportChains exports refs' chains as one export, as ExportChain exports
// each: the layers they lack blobs for build up to refCfg.Builds at once, and
// a layer the chains share builds once. chainCtxs, when not nil, holds each
// ref's own context, which the work done for its chain runs under. It
// returns the chains in refs' order; on error it returns none, and every
// chain it opened is released.
func ExportChains(ctx context.Context, refs []ImmutableRef, chainCtxs []context.Context, refCfg config.RefConfig) (_ []*ExportChain, rerr error) {
	var cm *snapshotManager
	own := make([]*immutableRef, 0, len(refs))
	for _, ref := range refs {
		sr, ok := ref.(*immutableRef)
		if !ok || sr == nil || (cm != nil && sr.cm != cm) {
			own = nil
			break
		}
		cm = sr.cm
		own = append(own, sr)
	}
	if len(own) > 0 {
		return cm.exportChains(ctx, own, chainCtxs, refCfg)
	}
	// Refs of other kinds, or of several managers, export one at a time.
	chains := make([]*ExportChain, 0, len(refs))
	defer func() {
		if rerr != nil {
			for _, chain := range chains {
				rerr = stderrors.Join(rerr, chain.Release(ctx))
			}
		}
	}()
	for i, ref := range refs {
		chainCtx := ctx
		if chainCtxs != nil {
			chainCtx = chainCtxs[i]
		}
		chain, err := ref.ExportChain(chainCtx, refCfg)
		if err != nil {
			return nil, err
		}
		chains = append(chains, chain)
	}
	return chains, nil
}

// chainExport is one chain of an export while it is opened.
type chainExport struct {
	chain *ExportChain
	// ctx carries the chain's pin, which leases what exporting it writes.
	ctx context.Context
	// snapshotIDs are the chain's snapshots, root first, and merged marks
	// those carrying the merge-usage label, for the export's stats: a merge,
	// a diff application, or a copy committed with its usage.
	snapshotIDs []string
	merged      map[string]bool
}

// layerBuild is ensuring one layer's blob, for the first chain of the export
// that has the layer.
type layerBuild struct {
	snapshotID string
	// parentSnapshotID is the snapshot the layer is diffed against, "" for
	// a chain's root.
	parentSnapshotID string
	owner            *chainExport

	desc     ocispecs.Descriptor
	hasLayer bool
	// computed is set when the build computed the layer's blob, as
	// ExportLayer.Computed is: diffed it, or converted a stored blob.
	computed    bool
	description string
	createdAt   time.Time
	stats       ExportStats
}

// exportChains exports refs' chains in three steps. It pins and walks each
// chain, planning one build per distinct layer; a layer's blob depends only
// on the layer and its parent, never on the parent's blob. It runs the
// builds, up to refCfg.Builds.Workers at once. Then, chain by chain and root
// first, it pins each layer's blob into the chain and records the chain's
// layers and stats, as exporting the chains one after another would: a
// layer an earlier chain built counts as reused.
func (cm *snapshotManager) exportChains(ctx context.Context, refs []*immutableRef, chainCtxs []context.Context, refCfg config.RefConfig) (_ []*ExportChain, rerr error) {
	exports := make([]*chainExport, 0, len(refs))
	defer func() {
		if rerr != nil {
			for _, export := range exports {
				_ = export.chain.pin.release(export.ctx)
			}
		}
	}()
	builds := map[string]*layerBuild{}
	var planned []*layerBuild
	for i, sr := range refs {
		chainCtx := ctx
		if chainCtxs != nil {
			chainCtx = chainCtxs[i]
		}
		pin, pinCtx, err := cm.newResourcePin(chainCtx)
		if err != nil {
			return nil, err
		}
		export := &chainExport{
			chain:  &ExportChain{Provider: cm.ContentStore, pin: pin},
			ctx:    pinCtx,
			merged: map[string]bool{},
		}
		exports = append(exports, export)
		if err := cm.AttachLease(pinCtx, pin.id, sr.SnapshotID()); err != nil {
			return nil, err
		}
		for snapshotID := sr.SnapshotID(); snapshotID != ""; {
			export.snapshotIDs = append(export.snapshotIDs, snapshotID)
			info, err := cm.Snapshotter.Stat(pinCtx, snapshotID)
			if err != nil {
				return nil, err
			}
			_, export.merged[snapshotID], _ = mergeUsageOf(info)
			snapshotID = info.Parent
		}
		slices.Reverse(export.snapshotIDs)
		export.chain.Layers = make([]ExportLayer, 0, len(export.snapshotIDs))
		export.chain.Prefixes = make(map[string]int, len(export.snapshotIDs))
		for i, snapshotID := range export.snapshotIDs {
			if i == 0 && isScratchSnapshotID(snapshotID) {
				continue
			}
			if builds[snapshotID] != nil {
				continue
			}
			build := &layerBuild{snapshotID: snapshotID, owner: export}
			if i > 0 {
				// Only a root actually omitted from the chain has the empty
				// reuse parent key. Keep its real ID for diffing the
				// following snapshot.
				build.parentSnapshotID = export.snapshotIDs[i-1]
			}
			builds[snapshotID] = build
			planned = append(planned, build)
		}
	}

	if err := cm.buildExportLayers(ctx, planned, refCfg); err != nil {
		return nil, err
	}

	chains := make([]*ExportChain, 0, len(exports))
	for _, export := range exports {
		if err := cm.assembleExportChain(export, builds, refCfg); err != nil {
			return nil, err
		}
		chains = append(chains, export.chain)
	}
	return chains, nil
}

// assembleExportChain pins each built layer's blob into the chain, root
// first, and records the chain's layers, prefixes and stats. The layers a
// chain built itself add their stats; one an earlier chain built counts as
// reused.
func (cm *snapshotManager) assembleExportChain(export *chainExport, builds map[string]*layerBuild, refCfg config.RefConfig) error {
	chain := export.chain
	var reuseParentID string
	for i, snapshotID := range export.snapshotIDs {
		if i == 0 && isScratchSnapshotID(snapshotID) {
			chain.Prefixes[snapshotID] = 0
			continue
		}
		build := builds[snapshotID]
		switch {
		case build.owner == export:
			chain.Stats.Add(build.stats)
			if export.merged[snapshotID] && build.stats.LayersDiffed > 0 {
				chain.Stats.LayersMerged += build.stats.LayersDiffed
				chain.Stats.MergedRawBytes += build.stats.RawBytes
				chain.Stats.MergedBlobBytes += build.stats.BlobBytes
				chain.Stats.MergedDiffTime += build.stats.DiffTime
			}
		case build.hasLayer:
			chain.Stats.LayersReused++
		}
		if build.hasLayer {
			desc := exportDescriptor(build.desc, refCfg.PreferNonDistributable)
			present, err := cm.pinContent(export.ctx, desc)
			if err != nil {
				return err
			}
			if !present {
				return errors.Wrapf(cerrdefs.ErrNotFound, "exported content %s disappeared", desc.Digest)
			}
			if err := cm.registerExportLayer(reuseParentID, snapshotID, desc); err != nil {
				return err
			}
			reuseParentID = snapshotID
			chain.Stats.Layers++
			createdAt := build.createdAt
			// The chain that built the layer computed its blob; for a later
			// chain it is reused, as for its stats.
			chain.Layers = append(chain.Layers, ExportLayer{
				Descriptor:  desc,
				Description: build.description,
				CreatedAt:   &createdAt,
				Computed:    build.computed && build.owner == export,
			})
		}
		chain.Prefixes[snapshotID] = len(chain.Layers)
	}
	return nil
}

// buildExportLayers ensures the planned layers' blobs, up to
// refCfg.Builds.Workers at once, each under its chain's context and pin. The
// first failure cancels the others.
func (cm *snapshotManager) buildExportLayers(ctx context.Context, planned []*layerBuild, refCfg config.RefConfig) error {
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(max(refCfg.Builds.Workers, 1))
	for _, build := range planned {
		eg.Go(func() error {
			if err := egCtx.Err(); err != nil {
				return err
			}
			buildCtx, cancel := context.WithCancelCause(build.owner.ctx)
			defer cancel(nil)
			stop := context.AfterFunc(egCtx, func() { cancel(context.Cause(egCtx)) })
			defer stop()
			return cm.buildExportLayer(buildCtx, build, refCfg)
		})
	}
	return eg.Wait()
}

func (cm *snapshotManager) buildExportLayer(ctx context.Context, build *layerBuild, refCfg config.RefConfig) error {
	opened, err := cm.GetBySnapshotID(ctx, build.snapshotID, NoUpdateLastUsed)
	if err != nil {
		return err
	}
	layer := opened.(*immutableRef)
	desc, hasLayer, computed, err := cm.ensureExportBlob(ctx, build.parentSnapshotID, layer, refCfg.Compression, refCfg.Builds.Gate, &build.stats)
	build.description = layer.GetDescription()
	build.createdAt = layer.GetCreatedAt()
	if releaseErr := layer.Release(context.WithoutCancel(ctx)); releaseErr != nil && err == nil {
		err = releaseErr
	}
	if err != nil {
		return err
	}
	build.desc, build.hasLayer, build.computed = desc, hasLayer, computed
	return nil
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

// getBlobWithCompressionWithRetry returns ref's blob in comp's compression, and
// reports whether it converted the stored blob to get it: a variant that is
// already present is returned as it is.
func getBlobWithCompressionWithRetry(ctx context.Context, ref *immutableRef, comp compression.Config) (_ ocispecs.Descriptor, converted bool, _ error) {
	if blobDesc, err := ref.getBlobWithCompression(ctx, comp.Type); err == nil {
		present, err := ref.cm.pinContent(ctx, blobDesc)
		if err != nil {
			return ocispecs.Descriptor{}, false, err
		}
		if present {
			return blobDesc, false, nil
		}
	} else if !cerrdefs.IsNotFound(err) {
		return ocispecs.Descriptor{}, false, err
	}
	converted, err := ensureCompression(ctx, ref, comp)
	if err != nil {
		return ocispecs.Descriptor{}, false, errors.Wrapf(err, "failed to ensure compression type %q", comp.Type)
	}
	desc, err := ref.getBlobWithCompression(ctx, comp.Type)
	return desc, converted, err
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
	cm.layerImportedLocked()
	return nil
}
