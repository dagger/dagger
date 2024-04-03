package mage

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/internal/mage/sdk"
)

type linter interface {
	Lint(context.Context) error
}

// Lint runs all linters
func Lint(ctx context.Context) error {
	targets := []linter{
		Engine{},
		Docs{},
		sdk.All{},
	}
	eg, ctx := errgroup.WithContext(ctx)

	for _, t := range targets {
		t := t
		eg.Go(func() error {
			return t.Lint(ctx)
		})
	}

	return eg.Wait()
}
