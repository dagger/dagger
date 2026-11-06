//go:build !linux

package snapshots

import (
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/diff/apply"
)

// NewLayerApplier returns containerd's file system applier: the engine's own
// extraction is Linux only.
func NewLayerApplier(store content.Provider) diff.Applier {
	return apply.NewFileSystemApplier(store)
}
