// A module that stashes away its context through intermediary types,
// demonstrating that the returned object (i.e. Chained#1) always runs with the
// freshest context.
package main

import (
	"context"
	"strings"

	"dagger/chained-context/internal/dagger"
)

type ChainedContext struct {
	Source *dagger.Directory // +private
}

func New(
	// +defaultPath="/"
	source *dagger.Directory,
) *ChainedContext {
	if source == nil {
		panic("no source?")
	}
	return &ChainedContext{
		Source: source,
	}
}

// Update the content of the marker file.
func (m *ChainedContext) UpdateMarker(ctx context.Context, value string) *dagger.Changeset {
	return m.Source.
		// make a non-telegraphed tweak to the marker to ensure the LLM doesn't just
		// hallucinate it
		WithNewFile("marker.txt", strings.ToUpper(value)+"!").
		Changes(m.Source)
}

// Update the content of the marker file.
func (m *ChainedContext) Reader() *Chained {
	return &Chained{
		Source: m.Source,
	}
}

type Chained struct {
	Source *dagger.Directory
}

// Call the leaf module to read the marker. If env workspace propagation works
// correctly, the leaf module should receive the same workspace context.
func (m *Chained) ReadMarker(ctx context.Context) (string, error) {
	return m.Source.File("marker.txt").Contents(ctx)
}
