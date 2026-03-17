// A module that reads from a directory with @defaultPath.
// This is the "leaf" module in the nested call chain.
package main

import (
	"context"
	"dagger/nested-context-leaf/internal/dagger"
)

type NestedContextLeaf struct {
	Source *dagger.Directory // +private
}

func New(
	// +defaultPath="/"
	source *dagger.Directory,
) *NestedContextLeaf {
	return &NestedContextLeaf{
		Source: source,
	}
}

// Read a marker file from the source directory.
func (m *NestedContextLeaf) ReadMarker(ctx context.Context) (string, error) {
	return m.Source.File("marker.txt").Contents(ctx)
}
