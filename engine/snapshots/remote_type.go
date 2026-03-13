package snapshots

import (
	"github.com/containerd/containerd/v2/core/content"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// Remote is a descriptor or a list of stacked descriptors that can be pulled
// from a content provider.
type Remote struct {
	Descriptors []ocispecs.Descriptor
	Provider    content.InfoReaderProvider
}
