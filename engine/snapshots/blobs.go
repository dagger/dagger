package snapshots

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strconv"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/plugins/diff/walking"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/dagger/dagger/internal/buildkit/util/converter"
	"github.com/dagger/dagger/internal/buildkit/util/flightcontrol"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/dagger/dagger/internal/buildkit/util/winlayers"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

var g flightcontrol.Group[*leaseutil.LeaseRef]
var gEnsureExportBlob flightcontrol.Group[ensureExportBlobResult]

var ErrNoBlobs = errors.Errorf("no blobs for snapshot")

type ensureExportBlobResult struct {
	desc     ocispecs.Descriptor
	hasLayer bool
}

//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func (cm *snapshotManager) ensureExportBlob(
	ctx context.Context,
	parentSnapshotID string,
	ref *immutableRef,
	refCfg compression.Config,
) (_ ocispecs.Descriptor, _ bool, rerr error) {
	if ref == nil {
		return ocispecs.Descriptor{}, false, errors.New("ensure export blob: nil ref")
	}

	if blobDigest := ref.md.getBlob(); blobDigest != "" {
		desc, err := ref.ociDesc(ctx, false)
		if err != nil {
			return ocispecs.Descriptor{}, false, err
		}
		if refCfg.Force {
			blobDesc, err := getBlobWithCompressionWithRetry(ctx, ref, refCfg)
			if err != nil {
				return ocispecs.Descriptor{}, false, err
			}
			desc.MediaType = blobDesc.MediaType
			desc.Digest = blobDesc.Digest
			desc.Size = blobDesc.Size
			desc.URLs = blobDesc.URLs
			desc.Annotations = maps.Clone(blobDesc.Annotations)
		}
		if err := cm.recordSnapshotContent(ref.SnapshotID(), desc); err != nil {
			return ocispecs.Descriptor{}, false, err
		}
		return desc, true, nil
	}

	level := ""
	if refCfg.Level != nil {
		level = strconv.Itoa(*refCfg.Level)
	}
	key := fmt.Sprintf(
		"ensureExportBlob-%s-%s-%s-%t-%s",
		ref.SnapshotID(),
		parentSnapshotID,
		refCfg.Type.String(),
		refCfg.Force,
		level,
	)
	result, err := gEnsureExportBlob.Do(ctx, key, func(ctx context.Context) (_ ensureExportBlobResult, err error) {
		if blobDigest := ref.md.getBlob(); blobDigest != "" {
			desc, err := ref.ociDesc(ctx, false)
			if err != nil {
				return ensureExportBlobResult{}, err
			}
			if refCfg.Force {
				blobDesc, err := getBlobWithCompressionWithRetry(ctx, ref, refCfg)
				if err != nil {
					return ensureExportBlobResult{}, err
				}
				desc.MediaType = blobDesc.MediaType
				desc.Digest = blobDesc.Digest
				desc.Size = blobDesc.Size
				desc.URLs = blobDesc.URLs
				desc.Annotations = maps.Clone(blobDesc.Annotations)
			}
			if err := cm.recordSnapshotContent(ref.SnapshotID(), desc); err != nil {
				return ensureExportBlobResult{}, err
			}
			return ensureExportBlobResult{
				desc:     desc,
				hasLayer: true,
			}, nil
		}

		usage, err := cm.Snapshotter.Usage(ctx, ref.SnapshotID())
		if err != nil && !cerrdefs.IsNotFound(err) {
			return ensureExportBlobResult{}, err
		}
		if parentSnapshotID == "" && usage.Size == 0 && usage.Inodes == 0 {
			return ensureExportBlobResult{}, nil
		}

		if _, ok := leases.FromContext(ctx); !ok {
			leaseCtx, done, err := leaseutil.WithLease(ctx, cm.LeaseManager, leaseutil.MakeTemporary)
			if err != nil {
				return ensureExportBlobResult{}, err
			}
			defer done(context.WithoutCancel(leaseCtx))
			ctx = leaseCtx
		}

		if isTypeWindows(ref) {
			ctx = winlayers.UseWindowsLayerMode(ctx)
		}

		compressorFunc, finalize := refCfg.Type.Compress(ctx, refCfg)
		mediaType := refCfg.Type.MediaType()

		var lower []mount.Mount
		if parentSnapshotID != "" {
			parentRef, err := cm.GetBySnapshotID(ctx, parentSnapshotID, NoUpdateLastUsed)
			if err != nil {
				return ensureExportBlobResult{}, err
			}
			defer func() {
				_ = parentRef.Release(context.WithoutCancel(ctx))
			}()

			mountable, err := parentRef.Mount(ctx, true)
			if err != nil {
				return ensureExportBlobResult{}, err
			}
			var release func() error
			lower, release, err = mountable.Mount()
			if err != nil {
				return ensureExportBlobResult{}, err
			}
			if release != nil {
				defer release()
			}
		}

		mountable, err := ref.Mount(ctx, true)
		if err != nil {
			return ensureExportBlobResult{}, err
		}
		upper, releaseUpper, err := mountable.Mount()
		if err != nil {
			return ensureExportBlobResult{}, err
		}
		if releaseUpper != nil {
			defer releaseUpper()
		}

		var desc ocispecs.Descriptor
		var enableOverlay, fallback, logWarnOnErr bool
		if forceOvlStr := os.Getenv("BUILDKIT_DEBUG_FORCE_OVERLAY_DIFF"); forceOvlStr != "" {
			enableOverlay, err = strconv.ParseBool(forceOvlStr)
			if err != nil {
				return ensureExportBlobResult{}, errors.Wrapf(err, "invalid boolean in BUILDKIT_DEBUG_FORCE_OVERLAY_DIFF")
			}
			fallback = false
		} else if !isTypeWindows(ref) {
			enableOverlay, fallback = true, true
			switch cm.Snapshotter.Name() {
			case "overlayfs", "stargz":
				logWarnOnErr = true
			case "fuse-overlayfs", "native":
				enableOverlay = false
			}
		}
		if enableOverlay {
			computed, ok, err := ref.tryComputeOverlayBlob(ctx, lower, upper, mediaType, ref.ID(), compressorFunc)
			if !ok || err != nil {
				if !fallback {
					if !ok {
						return ensureExportBlobResult{}, errors.Errorf("overlay mounts not detected (lower=%+v,upper=%+v)", lower, upper)
					}
					return ensureExportBlobResult{}, errors.Wrapf(err, "failed to compute overlay diff")
				}
				if logWarnOnErr {
					bklog.G(ctx).Warnf("failed to compute blob by overlay differ (ok=%v): %v", ok, err)
				}
			} else {
				desc = computed
			}
		}

		if desc.Digest == "" && !isTypeWindows(ref) && refCfg.Type.NeedsComputeDiffBySelf(refCfg) {
			desc, err = walking.NewWalkingDiff(cm.ContentStore).Compare(ctx, lower, upper,
				diff.WithMediaType(mediaType),
				diff.WithReference(ref.ID()),
				diff.WithCompressor(compressorFunc),
			)
			if err != nil {
				bklog.G(ctx).WithError(err).Warnf("failed to compute blob by buildkit differ")
			}
		}

		if desc.Digest == "" {
			desc, err = cm.Differ.Compare(ctx, lower, upper,
				diff.WithMediaType(mediaType),
				diff.WithReference(ref.ID()),
				diff.WithCompressor(compressorFunc),
			)
			if err != nil {
				return ensureExportBlobResult{}, err
			}
		}

		if desc.Annotations == nil {
			desc.Annotations = map[string]string{}
		}
		if finalize != nil {
			annotations, err := finalize(ctx, cm.ContentStore)
			if err != nil {
				return ensureExportBlobResult{}, errors.Wrapf(err, "failed to finalize compression")
			}
			maps.Copy(desc.Annotations, annotations)
		}
		info, err := cm.ContentStore.Info(ctx, desc.Digest)
		if err != nil {
			return ensureExportBlobResult{}, err
		}
		if diffID, ok := info.Labels[labels.LabelUncompressed]; ok {
			desc.Annotations[labels.LabelUncompressed] = diffID
		} else if mediaType == ocispecs.MediaTypeImageLayer {
			desc.Annotations[labels.LabelUncompressed] = desc.Digest.String()
		} else {
			return ensureExportBlobResult{}, errors.Errorf("unknown layer compression type")
		}
		info = addBlobDescToInfo(desc, info)
		if _, err := cm.ContentStore.Update(ctx, info, fieldsFromLabels(info.Labels)...); err != nil {
			return ensureExportBlobResult{}, err
		}

		diffID, err := diffIDFromDescriptor(desc)
		if err != nil {
			return ensureExportBlobResult{}, err
		}
		ref.mu.Lock()
		if err := ref.md.queueDiffID(diffID); err != nil {
			ref.mu.Unlock()
			return ensureExportBlobResult{}, err
		}
		if err := ref.md.queueBlob(desc.Digest); err != nil {
			ref.mu.Unlock()
			return ensureExportBlobResult{}, err
		}
		if err := ref.md.queueMediaType(desc.MediaType); err != nil {
			ref.mu.Unlock()
			return ensureExportBlobResult{}, err
		}
		if err := ref.md.queueBlobSize(desc.Size); err != nil {
			ref.mu.Unlock()
			return ensureExportBlobResult{}, err
		}
		if err := ref.md.queueBlobOnly(false); err != nil {
			ref.mu.Unlock()
			return ensureExportBlobResult{}, err
		}
		if err := ref.md.appendURLs(desc.URLs); err != nil {
			ref.mu.Unlock()
			return ensureExportBlobResult{}, err
		}
		if err := ref.md.commitMetadata(); err != nil {
			ref.mu.Unlock()
			return ensureExportBlobResult{}, err
		}
		ref.mu.Unlock()
		if err := cm.recordSnapshotContent(ref.SnapshotID(), desc); err != nil {
			return ensureExportBlobResult{}, err
		}
		return ensureExportBlobResult{
			desc:     desc,
			hasLayer: true,
		}, nil
	})
	if err != nil {
		return ocispecs.Descriptor{}, false, err
	}
	return result.desc, result.hasLayer, nil
}

func isTypeWindows(sr *immutableRef) bool {
	return sr.GetLayerType() == "windows"
}

// ensureCompression ensures the specified ref has the blob of the specified compression Type.
func ensureCompression(ctx context.Context, ref *immutableRef, comp compression.Config) error {
	l, err := g.Do(ctx, fmt.Sprintf("ensureComp-%s-%s", ref.ID(), comp.Type), func(ctx context.Context) (_ *leaseutil.LeaseRef, err error) {
		desc, err := ref.ociDesc(ctx, true)
		if err != nil {
			return nil, err
		}

		l, ctx, err := leaseutil.NewLease(ctx, ref.cm.LeaseManager, leaseutil.MakeTemporary)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err != nil {
				l.Discard()
			}
		}()

		// Resolve converters
		layerConvertFunc, err := converter.New(ctx, ref.cm.ContentStore, desc, comp)
		if err != nil {
			return nil, err
		} else if layerConvertFunc == nil {
			if err := ref.linkBlob(ctx, desc); err != nil {
				return nil, err
			}
			return l, nil
		}

		// First, lookup local content store
		if _, err := ref.getBlobWithCompression(ctx, comp.Type); err == nil {
			return l, nil // found the compression variant. no need to convert.
		}

		if _, err := ref.cm.ContentStore.Info(ctx, desc.Digest); err != nil {
			if cerrdefs.IsNotFound(err) {
				return nil, errors.New("missing local content blob")
			}
			return l, err
		}

		// Convert layer compression type.
		newDesc, err := layerConvertFunc(ctx, ref.cm.ContentStore, desc)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to convert")
		}

		// Start to track converted layer
		if err := ref.linkBlob(ctx, *newDesc); err != nil {
			return nil, errors.Wrapf(err, "failed to add compression blob")
		}
		return l, nil
	})
	if err != nil {
		return err
	}
	if l != nil {
		if err := l.Adopt(ctx); err != nil {
			return err
		}
	}
	return nil
}
