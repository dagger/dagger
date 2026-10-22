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
}

type ExportChain struct {
	Layers []ExportLayer
	// Prefixes maps each snapshot the export walked, from the root to the
	// exported one, to the number of Layers that rebuild it: the layers
	// emitted up to and including it. A snapshot that added no layer maps to
	// the prefix before it, and a scratch root to none.
	Prefixes map[string]int
	Provider content.InfoReaderProvider
	// Timing, when set, receives where ImportChain's time went.
	Timing *ImportTiming
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
	// archiving, compressing and writing the content.
	DiffTime time.Duration
	// LockWait is the wait for the per-snapshot export lock, held by another
	// export of the same layer.
	LockWait time.Duration
	// LayersMerged counts the diffed layers whose snapshot a merge or a diff
	// application made, and the Merged sums are their share of RawBytes,
	// BlobBytes and DiffTime.
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
	s.LayersMerged += o.LayersMerged
	s.MergedRawBytes += o.MergedRawBytes
	s.MergedBlobBytes += o.MergedBlobBytes
	s.MergedDiffTime += o.MergedDiffTime
}

// ImportTiming sums where a chain import's time went, over its layers. The
// layers are imported one at a time, and each blob is fetched whole before
// it is applied, so the three never overlap.
type ImportTiming struct {
	// LayerWait is the wait for the per-layer import lock, held by another
	// import of the same layer onto the same parent.
	LayerWait time.Duration
	// Fetch is getting blobs into the content store: reading them, writing
	// and verifying them, and committing them. A reused layer adds nothing.
	Fetch time.Duration
	// Apply is applying the fetched blobs to their snapshots.
	Apply time.Duration
}

// now is the time for a phase's start, read only when timing is on.
func (t *ImportTiming) now() time.Time {
	if t == nil {
		return time.Time{}
	}
	return time.Now()
}

// Release ends provider consumption. Copies of a locally exported chain share
// the same ownership, so Release is safe to call more than once.
func (c *ExportChain) Release(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return c.pin.release(ctx)
}
