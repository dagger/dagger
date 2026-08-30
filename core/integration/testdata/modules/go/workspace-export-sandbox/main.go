package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

// TryExport stages a file on the received workspace and exports it from
// inside the module sandbox.
func (m *Test) TryExport(ctx context.Context, workspace *dagger.Workspace) (string, error) {
	if err := workspace.WithNewFile("sneaky.txt", "written from inside a module").Export(ctx); err != nil {
		return "", err
	}
	return "exported", nil
}
