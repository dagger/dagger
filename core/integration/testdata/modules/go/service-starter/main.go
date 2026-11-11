package main

import (
	"context"
	"fmt"
	"time"

	"dagger/starter/internal/dagger"
)

type Starter struct{}

// Start brings svc up through a tunnel, as a client other than its caller,
// and keeps it up until the caller has bound it too, which the caller marks
// by creating /bound in cache.
func (m *Starter) Start(ctx context.Context, svc *dagger.Service, cache *dagger.CacheVolume) error {
	upCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	upErr := make(chan error, 1)
	go func() { upErr <- svc.Up(upCtx) }()

	_, err := dag.Container().From("alpine").
		WithMountedCache("/cache", cache).
		WithEnvVariable("CACHE", time.Now().String()).
		WithExec([]string{"timeout", "120", "sh", "-c", "until [ -e /cache/bound ]; do sleep 0.1; done"}).
		Sync(ctx)
	select {
	case upErr := <-upErr:
		return fmt.Errorf("service stopped before the caller bound it: %w", upErr)
	default:
	}
	return err
}
