package main

import (
	"context"

	"dagger/reader/internal/dagger"
)

type Reader struct {
	Source *dagger.Directory
}

func New(
	// +defaultPath="/"
	source *dagger.Directory,
) *Reader {
	return &Reader{Source: source}
}

func (m *Reader) Read(ctx context.Context) (string, error) {
	return m.Source.File("workspace-marker.txt").Contents(ctx)
}
