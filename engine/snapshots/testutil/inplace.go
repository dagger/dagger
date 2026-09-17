package testutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/containerd/v2/pkg/archive/compression"
	"github.com/containerd/containerd/v2/pkg/epoch"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/errdefs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// The store's applier and differ work on a native snapshot's directory in
// place, so that no test needs a mount system call and therefore no test needs
// root, a mount namespace or a privilege probe. containerd's own applier and
// walking differ mount even a writable bind (mount.WithTempMount), and
// LocalMounter mounts every read-only view.
//
// What stays real: containerd's local content store, native snapshotter,
// metadata database, lease manager and GarbageCollect, the engine's
// SnapshotManager, and the tar bytes, compression and layer digests, which
// are produced by the same archive and compression packages containerd uses.
//
// What tests on this store do not exercise: the two temporary mounts,
// LocalMounter's read-only mount, and any core reader that goes through it
// (File.Contents, Directory.Entries, Stat). Export's first choice for a
// compression type that must compute its own diff is a hard-coded walking
// differ (engine/snapshots/blobs.go, NeedsComputeDiffBySelf); unprivileged it
// fails to mount, logs a warning, and export falls back to this differ, so
// such a test passes through the fallback. The native cases in
// core/integration run all of these for real.
//
// inPlaceApplier and inPlaceDiffer follow containerd v2's
// core/diff/apply.fsApplier and plugins/diff/walking.walkingDiff step for
// step, replacing only the mount with the bind mount's source directory.

// bindSource returns the directory behind a native snapshot's single bind
// mount. No mounts means an empty lower layer.
func bindSource(mounts []mount.Mount) (string, func(), error) {
	if len(mounts) == 0 {
		dir, err := os.MkdirTemp("", "snapshot-test-empty")
		return dir, func() { os.RemoveAll(dir) }, err
	}
	if len(mounts) != 1 || (mounts[0].Type != "bind" && mounts[0].Type != "rbind") {
		return "", nil, fmt.Errorf("test store expects one native bind mount, got %+v", mounts)
	}
	return mounts[0].Source, func() {}, nil
}

// Root returns the directory behind a committed snapshot of this store, for a
// test that reads the bytes it expects there.
func Root(t testing.TB, ref bkcache.ImmutableRef) string {
	t.Helper()
	mounted, err := ref.Mount(context.Background(), true)
	require.NoError(t, err)
	mounts, release, err := mounted.Mount()
	require.NoError(t, err)
	if release != nil {
		require.NoError(t, release())
	}
	root, _, err := bindSource(mounts)
	require.NoError(t, err)
	return root
}

type inPlaceApplier struct{ store content.Provider }

func (a inPlaceApplier) Apply(ctx context.Context, desc ocispecs.Descriptor, mounts []mount.Mount, opts ...diff.ApplyOpt) (ocispecs.Descriptor, error) {
	var config diff.ApplyConfig
	for _, opt := range opts {
		if err := opt(ctx, desc, &config); err != nil {
			return ocispecs.Descriptor{}, fmt.Errorf("failed to apply config opt: %w", err)
		}
	}
	root, release, err := bindSource(mounts)
	if err != nil {
		return ocispecs.Descriptor{}, err
	}
	defer release()
	ra, err := a.store.ReaderAt(ctx, desc)
	if err != nil {
		return ocispecs.Descriptor{}, fmt.Errorf("failed to get reader from content store: %w", err)
	}
	defer ra.Close()
	processor := diff.NewProcessorChain(desc.MediaType, content.NewReader(ra))
	processors := []diff.StreamProcessor{processor}
	for {
		if processor, err = diff.GetProcessor(ctx, processor, config.ProcessorPayloads); err != nil {
			return ocispecs.Descriptor{}, fmt.Errorf("failed to get stream processor for %s: %w", desc.MediaType, err)
		}
		processors = append(processors, processor)
		if processor.MediaType() == ocispecs.MediaTypeImageLayer {
			break
		}
	}
	defer processor.Close()
	digester := digest.Canonical.Digester()
	counted := &readCounter{r: io.TeeReader(processor, digester.Hash())}
	if _, err := archive.Apply(ctx, root, counted); err != nil {
		return ocispecs.Descriptor{}, err
	}
	// Read any trailing data, as containerd does, so the digest covers it.
	if _, err := io.Copy(io.Discard, counted); err != nil {
		return ocispecs.Descriptor{}, err
	}
	for _, p := range processors {
		if failed, ok := p.(interface{ Err() error }); ok {
			if err := failed.Err(); err != nil {
				return ocispecs.Descriptor{}, err
			}
		}
	}
	return ocispecs.Descriptor{MediaType: ocispecs.MediaTypeImageLayer, Size: counted.n, Digest: digester.Digest()}, nil
}

type readCounter struct {
	r io.Reader
	n int64
}

func (c *readCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type inPlaceDiffer struct{ store content.Store }

func (d inPlaceDiffer) Compare(ctx context.Context, lower, upper []mount.Mount, opts ...diff.Opt) (_ ocispecs.Descriptor, rerr error) {
	var config diff.Config
	for _, opt := range opts {
		if err := opt(&config); err != nil {
			return ocispecs.Descriptor{}, err
		}
	}
	if tm := epoch.FromContext(ctx); tm != nil && config.SourceDateEpoch == nil {
		config.SourceDateEpoch = tm
	}
	var writeDiffOpts []archive.WriteDiffOpt
	if config.SourceDateEpoch != nil {
		writeDiffOpts = append(writeDiffOpts, archive.WithSourceDateEpoch(config.SourceDateEpoch))
	}
	compressionType := compression.Uncompressed
	if config.Compressor != nil {
		if config.MediaType == "" {
			return ocispecs.Descriptor{}, errors.New("media type must be explicitly specified when using custom compressor")
		}
		compressionType = compression.Unknown
	} else {
		if config.MediaType == "" {
			config.MediaType = ocispecs.MediaTypeImageLayerGzip
		}
		switch config.MediaType {
		case ocispecs.MediaTypeImageLayer:
		case ocispecs.MediaTypeImageLayerGzip:
			compressionType = compression.Gzip
		case ocispecs.MediaTypeImageLayerZstd:
			compressionType = compression.Zstd
		default:
			return ocispecs.Descriptor{}, fmt.Errorf("unsupported diff media type: %v: %w", config.MediaType, errdefs.ErrNotImplemented)
		}
	}
	lowerRoot, releaseLower, err := bindSource(lower)
	if err != nil {
		return ocispecs.Descriptor{}, err
	}
	defer releaseLower()
	upperRoot, releaseUpper, err := bindSource(upper)
	if err != nil {
		return ocispecs.Descriptor{}, err
	}
	defer releaseUpper()

	newReference := config.Reference == ""
	if newReference {
		config.Reference = "test-diff-" + digest.FromString(lowerRoot+"\x00"+upperRoot).Encoded()
	}
	writer, err := d.store.Writer(ctx, content.WithRef(config.Reference), content.WithDescriptor(ocispecs.Descriptor{MediaType: config.MediaType}))
	if err != nil {
		return ocispecs.Descriptor{}, fmt.Errorf("failed to open writer: %w", err)
	}
	defer func() {
		if rerr != nil {
			writer.Close()
			if newReference {
				_ = d.store.Abort(ctx, config.Reference)
			}
		}
	}()
	if !newReference {
		if err := writer.Truncate(0); err != nil {
			return ocispecs.Descriptor{}, err
		}
	}
	if compressionType != compression.Uncompressed {
		digester := digest.SHA256.Digester()
		var compressed io.WriteCloser
		if config.Compressor != nil {
			compressed, err = config.Compressor(writer, config.MediaType)
		} else {
			compressed, err = compression.CompressStream(writer, compressionType)
		}
		if err != nil {
			return ocispecs.Descriptor{}, fmt.Errorf("failed to get compressed stream: %w", err)
		}
		err = archive.WriteDiff(ctx, io.MultiWriter(compressed, digester.Hash()), lowerRoot, upperRoot, writeDiffOpts...)
		compressed.Close()
		if err != nil {
			return ocispecs.Descriptor{}, fmt.Errorf("failed to write compressed diff: %w", err)
		}
		if config.Labels == nil {
			config.Labels = map[string]string{}
		}
		config.Labels[labels.LabelUncompressed] = digester.Digest().String()
	} else if err := archive.WriteDiff(ctx, writer, lowerRoot, upperRoot, writeDiffOpts...); err != nil {
		return ocispecs.Descriptor{}, fmt.Errorf("failed to write diff: %w", err)
	}
	var commitOpts []content.Opt
	if config.Labels != nil {
		commitOpts = append(commitOpts, content.WithLabels(config.Labels))
	}
	dgst := writer.Digest()
	if err := writer.Commit(ctx, 0, dgst, commitOpts...); err != nil && !errdefs.IsAlreadyExists(err) {
		return ocispecs.Descriptor{}, fmt.Errorf("failed to commit: %w", err)
	}
	info, err := d.store.Info(ctx, dgst)
	if err != nil {
		return ocispecs.Descriptor{}, fmt.Errorf("failed to get info from content store: %w", err)
	}
	if info.Labels == nil {
		info.Labels = map[string]string{}
	}
	// Set the uncompressed label if the digest already existed without it.
	if _, ok := info.Labels[labels.LabelUncompressed]; !ok {
		info.Labels[labels.LabelUncompressed] = config.Labels[labels.LabelUncompressed]
		if _, err := d.store.Update(ctx, info, "labels."+labels.LabelUncompressed); err != nil {
			return ocispecs.Descriptor{}, fmt.Errorf("error setting uncompressed label: %w", err)
		}
	}
	return ocispecs.Descriptor{MediaType: config.MediaType, Size: info.Size, Digest: info.Digest}, nil
}
