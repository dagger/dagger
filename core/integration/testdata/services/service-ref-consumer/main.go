// A module whose constructor accepts optional core-type args, used to test
// wiring another module's function output in via
// workspace settings.
package main

import (
	"context"
	"fmt"

	"dagger/service-ref-consumer/internal/dagger"
)

type ServiceRefConsumer struct {
	App             *dagger.Service
	Base            *dagger.Container
	Directory       *dagger.Directory
	File            *dagger.File
	WorkspaceMarker *dagger.File
}

func New(
	// +optional
	app *dagger.Service,
	// +optional
	base *dagger.Container,
	// +optional
	directory *dagger.Directory,
	// +optional
	file *dagger.File,
	// +optional
	sourceWorkspace *dagger.Workspace,
) *ServiceRefConsumer {
	var workspaceMarker *dagger.File
	if sourceWorkspace != nil {
		workspaceMarker = sourceWorkspace.File("marker.txt")
	}
	return &ServiceRefConsumer{
		App: app, Base: base, Directory: directory, File: file,
		WorkspaceMarker: workspaceMarker,
	}
}

// Returns "true" if a Service was provided, "false" otherwise.
func (m *ServiceRefConsumer) HasService() string {
	if m.App == nil {
		return "false"
	}
	return "true"
}

// Returns the PROVIDED_BY env var of the provided container, or "none" if no
// container was provided.
func (m *ServiceRefConsumer) ContainerProvidedBy(ctx context.Context) (string, error) {
	if m.Base == nil {
		return "none", nil
	}
	return m.Base.EnvVariable(ctx, "PROVIDED_BY")
}

// Returns the contents of the marker in the provided directory, or "none" if
// no directory was provided.
func (m *ServiceRefConsumer) DirectoryProvidedBy(ctx context.Context) (string, error) {
	if m.Directory == nil {
		return "none", nil
	}
	return m.Directory.File("provided-by").Contents(ctx)
}

// Returns the contents of the provided file, or "none" if no file was
// provided.
func (m *ServiceRefConsumer) FileProvidedBy(ctx context.Context) (string, error) {
	if m.File == nil {
		return "none", nil
	}
	return m.File.Contents(ctx)
}

// Returns the contents of the marker in the provided workspace, or "none" if
// no workspace was provided.
func (m *ServiceRefConsumer) WorkspaceProvidedBy(ctx context.Context) (string, error) {
	if m.WorkspaceMarker == nil {
		return "none", nil
	}
	return m.WorkspaceMarker.Contents(ctx)
}

// CheckService passes only when a Service was provided, used to test that
// settings-wired constructor args resolve under `dagger check` (both filtered
// and unfiltered) — checks run through the ModTree path, not the client's
// session schema, which historically broke module-ref resolution.
// +check
func (m *ServiceRefConsumer) CheckService() error {
	if m.App == nil {
		return fmt.Errorf("no service provided")
	}
	return nil
}

// Returns a Container, used to test wiring this module's function output into
// another module's constructor via workspace settings (e.g. reference cycles).
func (m *ServiceRefConsumer) Ctr() *dagger.Container {
	base := m.Base
	if base == nil {
		base = dag.Container().From("alpine:3.22.1")
	}
	return base.WithEnvVariable("PROVIDED_BY", "service-ref-consumer")
}
