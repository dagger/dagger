package main

import (
	"context"
	"crypto/rand"
	"strconv"
	"strings"

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

// Recorded is a check that takes the workspace like a lint check does, and
// records each run of its body, so tests can count how often it really ran.
// +check
func (m *Probe) Recorded(ctx context.Context, ws *dagger.Workspace) error {
	log, err := runLog(ctx, ws)
	if err != nil {
		return err
	}
	_, err = log.
		WithEnvVariable("NONCE", rand.Text()).
		WithExec([]string{"sh", "-c", "echo recorded >> /runs/log"}).
		Sync(ctx)
	return err
}

// Runs counts the recorded runs of Recorded's body.
// +cache="never"
func (m *Probe) Runs(ctx context.Context, ws *dagger.Workspace) (int, error) {
	log, err := runLog(ctx, ws)
	if err != nil {
		return 0, err
	}
	out, err := log.
		WithEnvVariable("NONCE", rand.Text()).
		WithExec([]string{"sh", "-c", "grep -cx recorded /runs/log || true"}).
		Stdout(ctx)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// runLog keeps the record of runs in a cache volume named after the
// workspace's nonce, so tests with different content don't share it.
func runLog(ctx context.Context, ws *dagger.Workspace) (*dagger.Container, error) {
	nonce, err := ws.File("nonce.txt").Contents(ctx)
	if err != nil {
		return nil, err
	}
	return dag.Container().
		From("alpine:3").
		WithMountedCache("/runs", dag.CacheVolume("workspace-identity-runs-"+strings.TrimSpace(nonce))), nil
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
