package main

import (
	"context"
)

// Directory is named after the core Directory type, and calls the core
// directory root field.
type Directory struct{}

func (m *Directory) Text(ctx context.Context) (string, error) {
	return dag.Directory().WithNewFile("a", "core directory").File("a").Contents(ctx)
}
