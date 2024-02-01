package mage

import (
	"context"

	"github.com/dagger/dagger/internal/mage/sdk"
	"golang.org/x/sync/errgroup"
)

type generator interface {
	Generate(context.Context) error
}

// Generate runs all generators
func Generate(ctx context.Context) error {
	targets := []generator{
		Docs{},
		sdk.All{},
	}
	eg, ctx := errgroup.WithContext(ctx)

	for _, t := range targets {
		t := t
		eg.Go(func() error {
			return t.Generate(ctx)
		})
	}

	return eg.Wait()
}
