package snapshots

import (
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
}
