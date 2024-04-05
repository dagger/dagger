package mage

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/ci/mage/util"
)

// Lint runs all linters
func Lint(ctx context.Context) error {
	targets := [][]string{
		{"engine"},
		{"docs"},
		{"sdk", "go"},
		{"sdk", "python"},
		{"sdk", "typescript"},
		{"sdk", "elixir"},
		{"sdk", "rust"},
		{"sdk", "php"},
	}

	eg, ctx := errgroup.WithContext(ctx)
	for _, target := range targets {
		target := append([]string{}, target...)
		target = append(target, "lint")

		eg.Go(func() error {
			return util.DaggerCall(ctx, target...)
		})
	}

	return eg.Wait()
}
