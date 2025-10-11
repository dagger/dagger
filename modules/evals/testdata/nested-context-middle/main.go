// A module that uses @defaultPath and calls another module.
// This is the "middle" module in the nested call chain.
package main

import (
	"context"
	"dagger/nested-context-middle/internal/dagger"
	"fmt"
	"strings"
)

type NestedContextMiddle struct {
	Source *dagger.Directory // +private
}

func New(
	// +defaultPath="/"
	source *dagger.Directory,
) *NestedContextMiddle {
	return &NestedContextMiddle{
		Source: source,
	}
}

// Update the content of the marker file.
func (m *NestedContextMiddle) UpdateMarker(ctx context.Context, value string) *dagger.Changeset {
	return m.Source.
		// make a non-telegraphed tweak to the marker to ensure the LLM doesn't just
		// hallucinate it
		WithNewFile("marker.txt", strings.ToUpper(value)+"!").
		Changes(m.Source)
}

// Call the leaf module to read the marker. If env workspace propagation works
// correctly, the leaf module should receive the same workspace context.
func (m *NestedContextMiddle) ReadMarker(ctx context.Context) string {
	nested, err := dag.NestedContextLeaf().ReadMarker(ctx)
	if err != nil {
		nested = fmt.Sprintf("<error: %s>", err)
	}
	middle, err := m.Source.File("marker.txt").Contents(ctx)
	if err != nil {
		middle = fmt.Sprintf("<error: %s>", err)
	}
	return "nested: " + nested + ", middle: " + middle
}
