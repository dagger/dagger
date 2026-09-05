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
	Layers   []ExportLayer
	Provider content.InfoReaderProvider
	pin      *resourcePin
}

// Release ends provider consumption. Copies of a locally exported chain share
// the same ownership, so Release is safe to call more than once.
func (c *ExportChain) Release(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return c.pin.release(ctx)
}
