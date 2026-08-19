// A module whose constructor declares a required Workspace argument, used to
// test that workspace settings can still wire its function output into another
// module's constructor: the engine builds that call by hand, and a required
// Workspace! must be supplied before dagql's non-null check rather than
// injected after it.
package main

import (
	"context"
	"strings"

	"dagger/workspace-container-provider/internal/dagger"
)

type WorkspaceContainerProvider struct {
	// Contents of the workspace's marker.txt, captured at construction (a
	// Workspace itself can't be stored as an object field).
	Marker string
}

func New(ctx context.Context, ws *dagger.Workspace) (*WorkspaceContainerProvider, error) {
	marker, err := readMarker(ctx, ws)
	if err != nil {
		return nil, err
	}
	return &WorkspaceContainerProvider{Marker: marker}, nil
}

// Returns a container annotated with this module's name and the marker read
// from the workspace passed to the constructor, proving it received the
// caller's workspace.
func (m *WorkspaceContainerProvider) Image() *dagger.Container {
	return image(m.Marker)
}

// Same as Image, but the Workspace is declared on the function rather than
// (only) the constructor.
func (m *WorkspaceContainerProvider) ImageFor(ctx context.Context, ws *dagger.Workspace) (*dagger.Container, error) {
	marker, err := readMarker(ctx, ws)
	if err != nil {
		return nil, err
	}
	return image(marker), nil
}

func readMarker(ctx context.Context, ws *dagger.Workspace) (string, error) {
	marker, err := ws.Directory(".").File("marker.txt").Contents(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(marker), nil
}

func image(marker string) *dagger.Container {
	return dag.Container().From("alpine:3.22.1").
		WithEnvVariable("PROVIDED_BY", "workspace-container-provider:"+marker)
}
