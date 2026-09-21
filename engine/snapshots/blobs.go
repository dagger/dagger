package snapshots

import (
	"context"
	"maps"
	"os"
	"strconv"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/plugins/diff/walking"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/dagger/dagger/internal/buildkit/util/converter"
	"github.com/dagger/dagger/internal/buildkit/util/winlayers"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

var ErrNoBlobs = errors.Errorf("no blobs for snapshot")

type ensureExportBlobResult struct {
	desc     ocispecs.Descriptor
	hasLayer bool
}

// snapshotBlobGCLabel is the label on a layer snapshot that names the blob
// its content was applied from, or was last diffed into. It is the durable
// record of which blob belongs to a snapshot: AttachLease reads it when a
// lease takes a snapshot chain, and adds the blob as a content resource of
// that lease, so every lease that names a snapshot names its blob too,
// including after a restart. The label alone protects the blob only from a
// non-flat lease, since containerd's collector skips label references of
// snapshots held by flat leases, which the engine's owner leases and pins
// are; that is why the content resource is added. A snapshot has one blob
// at a time; a new diff replaces the label.
const snapshotBlobGCLabel = "containerd.io/gc.ref.content.blob"

// labelSnapshotBlob binds blob to the snapshot for garbage collection.
func (cm *snapshotManager) labelSnapshotBlob(ctx context.Context, snapshotID string, blob digest.Digest) error {
	if snapshotID == "" || blob == "" {
		return nil
	}
	_, err := cm.Snapshotter.Update(ctx, snapshots.Info{
		Name:   snapshotID,
		Labels: map[string]string{snapshotBlobGCLabel: blob.String()},
	}, "labels."+snapshotBlobGCLabel)
	if err != nil {
		return errors.Wrapf(err, "label snapshot %s with blob %s", snapshotID, blob)
	}
	return nil
}

// restoreRecordedBlobFromLabel reads the snapshot's blob label and, when
// the blob is still in the content store with its stored descriptor,
// re-queues the ref's blob metadata from it and returns the digest. An
// unlabeled snapshot, or one whose blob is gone, returns "" so the caller
// diffs as before.
func (cm *snapshotManager) restoreRecordedBlobFromLabel(ctx context.Context, ref *immutableRef) (digest.Digest, error) {
	info, err := cm.Snapshotter.Stat(ctx, ref.SnapshotID())
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return "", nil
		}
		return "", errors.Wrapf(err, "stat snapshot %s for its blob label", ref.SnapshotID())
	}
	labeled := info.Labels[snapshotBlobGCLabel]
	if labeled == "" {
		return "", nil
	}
	blob := digest.Digest(labeled)
	if err := blob.Validate(); err != nil {
		return "", errors.Wrapf(err, "blob label of snapshot %s", ref.SnapshotID())
	}
	desc, err := getBlobDesc(ctx, cm.ContentStore, blob)
	if cerrdefs.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	diffID, err := diffIDFromDescriptor(desc)
	if err != nil {
		return "", err
	}
	ref.mu.Lock()
	defer ref.mu.Unlock()
	for _, queue := range []error{
		ref.md.queueDiffID(diffID),
		ref.md.queueBlob(desc.Digest),
		ref.md.queueMediaType(desc.MediaType),
		ref.md.queueBlobSize(desc.Size),
		ref.md.queueBlobOnly(false),
		ref.md.appendURLs(desc.URLs),
		ref.md.commitMetadata(),
	} {
		if queue != nil {
			return "", queue
		}
	}
	return desc.Digest, nil
}

//nolint:gocyclo // Keep blob reuse, diff computation and metadata commit in one export flow.
func (cm *snapshotManager) ensureExportBlob(
	ctx context.Context,
	parentSnapshotID string,
	ref *immutableRef,
	refCfg compression.Config,
) (_ ocispecs.Descriptor, _ bool, rerr error) {
	if ref == nil {
		return ocispecs.Descriptor{}, false, errors.New("ensure export blob: nil ref")
	}

	// A caller owns its own pins while waiting and doing I/O. Serializing by
	// snapshot avoids sharing a canceled caller's ref or lease with waiters.
	unlock, err := cm.exportLayerLocker.acquire(ctx, ref.SnapshotID())
	if err != nil {
		return ocispecs.Descriptor{}, false, err
	}
	defer unlock()
	result, err := func() (_ ensureExportBlobResult, err error) {
		blobDigest := ref.md.getBlob()
		if blobDigest == "" {
			// A ref reopened after a restart is rehydrated with no blob
			// record; the snapshot's label is the durable record. Restore
			// the metadata from it and the blob's stored descriptor.
			restored, err := cm.restoreRecordedBlobFromLabel(ctx, ref)
			if err != nil {
				return ensureExportBlobResult{}, err
			}
			blobDigest = restored
		}
		if blobDigest != "" {
			present, err := cm.pinContent(ctx, ocispecs.Descriptor{Digest: blobDigest})
			if err != nil {
				return ensureExportBlobResult{}, err
			}
			if present {
				desc, err := ref.ociDesc(ctx, false)
				if err != nil {
					return ensureExportBlobResult{}, err
				}
				if refCfg.Force {
					desc, err = getBlobWithCompressionWithRetry(ctx, ref, refCfg)
					if err != nil {
						return ensureExportBlobResult{}, err
					}
				}
				// Reuse repairs the label, so an update that failed after the
				// metadata was committed is not left missing. The label names
				// the recorded blob, not the compression variant a forced
				// export may have returned: the variant is a separate blob
				// linked to the recorded one, and the next ordinary export
				// asks for the recorded one.
				if err := cm.labelSnapshotBlob(ctx, ref.SnapshotID(), blobDigest); err != nil {
					return ensureExportBlobResult{}, err
				}
				if err := cm.recordSnapshotContent(ref.SnapshotID(), desc); err != nil {
					return ensureExportBlobResult{}, err
				}
				return ensureExportBlobResult{desc: desc, hasLayer: true}, nil
			}
		}

		usage, err := cm.Snapshotter.Usage(ctx, ref.SnapshotID())
		if err != nil && !cerrdefs.IsNotFound(err) {
			return ensureExportBlobResult{}, err
		}
		if parentSnapshotID == "" && usage.Size == 0 && usage.Inodes == 0 {
			return ensureExportBlobResult{}, nil
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
		// Before the blob metadata is committed: a failed label leaves no
		// reusable blob behind, and the next export diffs again.
		if err := cm.labelSnapshotBlob(ctx, ref.SnapshotID(), desc.Digest); err != nil {
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
	}()
	if err != nil {
		return ocispecs.Descriptor{}, false, err
	}
	return result.desc, result.hasLayer, nil
}

func isTypeWindows(sr *immutableRef) bool {
	return sr.GetLayerType() == "windows"
}

// ensureCompression ensures the specified ref has the blob of the specified compression Type.
// The caller holds exportLayerLocker and a private resource lease. Both the
// source blob and the result remain pinned through later provider consumption.
func ensureCompression(ctx context.Context, ref *immutableRef, comp compression.Config) error {
	desc, err := ref.ociDesc(ctx, true)
	if err != nil {
		return err
	}
	layerConvertFunc, err := converter.New(ctx, ref.cm.ContentStore, desc, comp)
	if err != nil {
		return err
	}
	if layerConvertFunc == nil {
		return ref.linkBlob(ctx, desc)
	}
	newDesc, err := layerConvertFunc(ctx, ref.cm.ContentStore, desc)
	if err != nil {
		return errors.Wrapf(err, "failed to convert")
	}
	return ref.linkBlob(ctx, *newDesc)
}
