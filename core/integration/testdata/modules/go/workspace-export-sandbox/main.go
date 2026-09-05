package main

import (
	"context"
	"fmt"

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

// TryExportTo exercises the explicit destination with an engine-side source.
// The destination is still a path on this module's client, not host authority.
func (m *Test) TryExportTo(ctx context.Context, workspace *dagger.Workspace) (string, error) {
	source := workspace.Git().Head().AsWorkspace().WithNewFile("sneaky.txt", "written from inside a module")
	err := source.Export(ctx, dagger.WorkspaceExportOpts{To: workspace})
	if err == nil {
		return "", fmt.Errorf("expected explicit export to refuse a checkout absent from the module sandbox")
	}
	return "refused", nil
}
