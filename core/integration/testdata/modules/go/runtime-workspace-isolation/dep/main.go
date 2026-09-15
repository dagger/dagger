package main

import (
	"context"
	"fmt"

	"dagger/dep/internal/dagger"
)

type Dep struct{}

func (m *Dep) ReadWorkspaceArg(
	ctx context.Context,
	// +optional
	workspace *dagger.Workspace,
) (string, error) {
	if workspace == nil {
		return "", fmt.Errorf("workspace argument was not passed")
	}
	return workspace.File("marker.txt").Contents(ctx)
}
