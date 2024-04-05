package mage

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/ci/mage/sdk"
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
