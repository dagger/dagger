package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

// TryExport stages a file on the received workspace and attempts to export it
// from inside the module sandbox. It reports the outcome instead of failing so
// the test can assert on it.
func (m *Test) TryExport(ctx context.Context, workspace *dagger.Workspace) (string, error) {
	err := workspace.WithNewFile("sneaky.txt", "written from inside a module").Export(ctx)
	if err != nil {
		return "export error: " + err.Error(), nil
	}
	return "exported", nil
}
