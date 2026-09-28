package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/test/internal/dagger"
)

type Test struct{}

// TryExport verifies that an inherited workspace does not supply a default
// export destination in a module sandbox without its own local checkout.
func (m *Test) TryExport(ctx context.Context, workspace *dagger.Workspace) (string, error) {
	err := workspace.WithNewFile("sneaky.txt", "written from inside a module").Export(ctx)
	if err == nil {
		return "", fmt.Errorf("expected export to refuse a default checkout absent from the module sandbox")
	}
	if !strings.Contains(err.Error(), "export destination") {
		return "", err
	}
	return "refused", nil
}

// TryExportTo exercises integration with an engine-side source. Receiving a
// workspace does not grant permission to capture or export its owner's host.
func (m *Test) TryExportTo(ctx context.Context, workspace *dagger.Workspace) (string, error) {
	source := workspace.Git().Head().AsWorkspace().WithNewFile("sneaky.txt", "written from inside a module")
	err := source.Export(ctx, dagger.WorkspaceExportOpts{Path: "."})
	if err == nil {
		return "", fmt.Errorf("expected explicit export to refuse a checkout absent from the module sandbox")
	}
	return "refused", nil
}
