package mage

import (
	"context"

	"github.com/dagger/dagger/internal/mage/sdk"
	"golang.org/x/sync/errgroup"
)

type tester interface {
	Test(context.Context) error
}

// Test runs all tests
func Test(ctx context.Context) error {
	targets := []tester{
		Engine{},
		sdk.All{},
	}
	eg, ctx := errgroup.WithContext(ctx)

	for _, t := range targets {
		t := t
		eg.Go(func() error {
			return t.Test(ctx)
		})
	}

	return eg.Wait()
}
