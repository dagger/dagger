package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"dagger/test/internal/dagger"
)

type Test struct{}

// Holder returns an object whose result is cached across sessions by default
// function caching; the seed arg gives each test run a distinct identity. The
// nonce is drawn at construction, so equal nonces across sessions prove the
// cached object was reused rather than reconstructed.
func (*Test) Holder(seed string) (*Holder, error) {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return &Holder{Seed: seed, Nonce: hex.EncodeToString(nonce)}, nil
}

type Holder struct {
	Seed  string
	Nonce string
}

// Use runs in the module's container runtime against the cached Holder state.
// The salt arg busts this function's cache so it executes in every session.
// It reads the module's own source through currentModule, a contextual
// default directory from the module context, and a module-scoped cache
// volume that accumulates every salt it has seen.
func (h *Holder) Use(
	ctx context.Context,
	salt string,
	// +defaultPath="/data"
	data *dagger.Directory,
) (string, error) {
	marker, err := dag.CurrentModule().Source().File("marker.txt").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("current module source: %w", err)
	}
	contextual, err := data.File("hello.txt").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("contextual default: %w", err)
	}
	seen, err := dag.Container().
		From("alpine:3.22.1").
		WithMountedCache("/cache", dag.CacheVolume("holder-seen")).
		WithExec([]string{"sh", "-c", "echo $0 >> /cache/salts && cat /cache/salts", salt}).
		Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("module cache volume: %w", err)
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s", h.Seed, h.Nonce, salt, strings.TrimSpace(marker), strings.TrimSpace(contextual), strings.ReplaceAll(strings.TrimSpace(seen), "\n", ",")), nil
}
