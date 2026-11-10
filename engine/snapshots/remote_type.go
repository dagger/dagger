package snapshots

import (
	"context"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type ExportLayer struct {
	Descriptor  ocispecs.Descriptor
	Description string
	CreatedAt   *time.Time
	// Computed is set when the export computed the layer's blob: it diffed
	// and compressed the layer's snapshot, or converted the snapshot's
	// stored blob to the export's compression. It is clear when the export
	// used a blob that already existed, such as a pulled image layer's or
	// one an earlier export computed.
	Computed bool
}

type ExportChain struct {
	Layers []ExportLayer
	// Prefixes maps each snapshot the export walked, from the root to the
	// exported one, to the number of Layers that rebuild it: the layers
	// emitted up to and including it. A snapshot that added no layer maps to
	// the prefix before it, and a scratch root to none.
	Prefixes map[string]int
	Provider content.InfoReaderProvider
	// Await, when set, is how the blobs of ImportChain's layers arrive, with
	// no Provider: each layer the engine hasn't imported yet waits for Await
	// to return before taking its blob from the content store, and nothing
	// is downloaded ahead. Await returns once the blob is in the content
	// store. A layer that waits holds no import lock, and stops waiting, its
	// Await's context canceled, once another import has imported it.
	Await func(ctx context.Context, desc ocispecs.Descriptor) error
	// Timing, when set, receives where ImportChain's time went.
	Timing *ImportTiming
	// Applied, when set, is told of each layer ImportChain applies itself,
	// with the snapshot it committed, while it still holds the layer's
	// import lock: before any other import can reuse the snapshot.
	Applied func(ctx context.Context, desc ocispecs.Descriptor, snapshotID string)
	// Stats are what exporting the chain took.
	Stats ExportStats
	pin   *resourcePin
}

// ExportStats sum what exporting chains took, over their layers.
type ExportStats struct {
	// Layers counts the layers emitted.
	Layers int
	// LayersReused counts the layers whose blob already existed, and
	// LayersDiffed those whose blob was computed.
	LayersReused int
	LayersDiffed int
	// RawBytes is the on-disk usage of the diffed layers' snapshots, and
	// BlobBytes the size of the blobs computed from them.
	RawBytes  int64
	BlobBytes int64
	// DiffTime is computing the diffed layers' blobs: reading the mounts,
	// archiving, compressing and writing the content. Layers that build at
	// once each add their own, so it can exceed the export's wall time.
	DiffTime time.Duration
	// LockWait is the wait for the per-snapshot export lock, held by another
	// export of the same layer.
	LockWait time.Duration
	// BuildWait is the wait for a build slot, shared with other exports, to
	// build the diffed layers' blobs.
	BuildWait time.Duration
	// LayersMerged counts the diffed layers whose snapshot carries the
	// merge-usage label: a merge, a diff application, or a copy committed with
	// its usage (CommitWithUsage). The Merged sums are their share of
	// RawBytes, BlobBytes and DiffTime.
	LayersMerged    int
	MergedRawBytes  int64
	MergedBlobBytes int64
	MergedDiffTime  time.Duration
}

// Add adds o to s.
func (s *ExportStats) Add(o ExportStats) {
	s.Layers += o.Layers
	s.LayersReused += o.LayersReused
	s.LayersDiffed += o.LayersDiffed
	s.RawBytes += o.RawBytes
	s.BlobBytes += o.BlobBytes
	s.DiffTime += o.DiffTime
	s.LockWait += o.LockWait
	s.BuildWait += o.BuildWait
	s.LayersMerged += o.LayersMerged
	s.MergedRawBytes += o.MergedRawBytes
	s.MergedBlobBytes += o.MergedBlobBytes
	s.MergedDiffTime += o.MergedDiffTime
}

// ImportTiming sums where a chain import's time went, over its layers. Each
// layer is fetched whole before it is applied. With fetching ahead, the next
// layers' blobs download while a layer applies. On a snapshotter that
// applies a chain's layers at once (overlayfs), the layers' fetches and
// applies overlap one another, so the sums can exceed the import's time; a
// chain applied in order spends them one after another, on its own path
// (Fetch is its wait for the blob, not the download's duration), and they
// never exceed it.
type ImportTiming struct {
	// LayerWait is the wait for the per-layer import lock, held by another
	// import of the same layer onto the same parent, or of the same layers.
	LayerWait time.Duration
	// Fetch is getting blobs into the content store: reading them, writing
	// and verifying them, and committing them. A reused layer adds nothing.
	Fetch time.Duration
	// Apply is applying the fetched blobs to their snapshots.
	Apply time.Duration
	// Ahead counts the blobs whose download started early, while the layer
	// before them applied, and Joined the layers whose blob came from a
	// download another import had in progress.
	Ahead  int
	Joined int
	// Packed counts the layers whose blob the import got with its pack's:
	// read, joined, or held from an earlier read.
	Packed int
}

// add adds o to t.
func (t *ImportTiming) add(o ImportTiming) {
	t.LayerWait += o.LayerWait
	t.Fetch += o.Fetch
	t.Apply += o.Apply
	t.Ahead += o.Ahead
	t.Joined += o.Joined
	t.Packed += o.Packed
}

// Release ends provider consumption. Copies of a locally exported chain share
// the same ownership, so Release is safe to call more than once.
func (c *ExportChain) Release(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return c.pin.release(ctx)
}
