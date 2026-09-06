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

// TryExportTo exercises integration with an engine-side source. Receiving a
// workspace does not grant permission to capture or export its owner's host.
func (m *Test) TryExportTo(ctx context.Context, workspace *dagger.Workspace) (string, error) {
	source := workspace.Git().Head().AsWorkspace().WithNewFile("sneaky.txt", "written from inside a module")
	err := workspace.WithCommitsFrom(source).Export(ctx)
	if err == nil {
		return "", fmt.Errorf("expected explicit export to refuse a checkout absent from the module sandbox")
	}
	return "refused", nil
}
