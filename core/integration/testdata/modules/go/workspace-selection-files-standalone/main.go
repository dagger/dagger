package main

import (
	"context"

	"dagger/files/internal/dagger"
)

type Files struct{}

func (m *Files) ReadWorkspaceArg(ctx context.Context, workspace *dagger.Workspace) (string, error) {
	return workspace.File("marker.txt").Contents(ctx)
}

func (m *Files) ReadCurrentWorkspace(ctx context.Context) (string, error) {
	return dag.CurrentWorkspace().File("marker.txt").Contents(ctx)
}

func (m *Files) ChangeWorkspaceArg(workspace *dagger.Workspace) *dagger.Changeset {
	before := workspace.Directory(".")
	after := before.WithNewFile("workspace-arg.txt", "changed through workspace arg")
	return after.Changes(before)
}

func (m *Files) ChangeCurrentWorkspace() *dagger.Changeset {
	before := dag.CurrentWorkspace().Directory(".")
	after := before.WithNewFile("current-workspace.txt", "changed through current workspace")
	return after.Changes(before)
}

func (m *Files) ChangeStandalone() *dagger.Changeset {
	before := dag.Directory()
	after := before.WithNewFile("standalone.txt", "changed without workspace")
	return after.Changes(before)
}

func (m *Files) ReturnedDirectory() *dagger.Directory {
	return dag.Directory().WithNewFile("returned-dir.txt", "returned directory")
}

func (m *Files) ReturnedFile() *dagger.File {
	return dag.Directory().WithNewFile("returned-file.txt", "returned file").File("returned-file.txt")
}

func (m *Files) ReturnedContainer() *dagger.Container {
	return dag.Container().
		From("alpine:3.22.1").
		WithExec([]string{"sh", "-c", "printf 'returned container' > /returned-container.txt"})
}
