package main

import (
	"context"
	"crypto/rand"

	"dagger/probe/internal/dagger"
)

// Probe holds the workspace it was constructed with, as CI modules do.
type Probe struct {
	// +private
	Ws *dagger.Workspace
}

func New(ws *dagger.Workspace) *Probe {
	return &Probe{Ws: ws}
}

// Check takes the workspace like a lint check does. Its result changes
// whenever its body runs, so two calls return the same value only when the
// second was a cache hit.
func (m *Probe) Check(ws *dagger.Workspace) string {
	return rand.Text()
}

// Verify is a check, so artifact discovery lists this module.
// +check
func (m *Probe) Verify() error {
	return nil
}

// Config reads the effective config through the workspace this object was
// constructed with. Each nonce makes a call that runs its body.
func (m *Probe) Config(ctx context.Context, nonce string) (string, error) {
	return m.Ws.ConfigRead(ctx, dagger.WorkspaceConfigReadOpts{Effective: true})
}

// ArtifactModules lists the modules artifact discovery finds through the
// workspace this object was constructed with. Each nonce makes a call that
// runs its body.
func (m *Probe) ArtifactModules(ctx context.Context, nonce string) ([]string, error) {
	mods, err := m.Ws.Artifacts().Modules(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(mods))
	for _, mod := range mods {
		name, err := mod.Name(ctx)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}
