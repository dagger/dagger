package mage

import (
	"context"

	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
)

type Dagger mg.Namespace

// Publish publishes Engine and CLI together - CLI depends on Engine
func (Dagger) Publish(ctx context.Context, version string) error {
	err := Engine{}.Publish(ctx, version)
	if err != nil {
		return err
	}

	err = Cli{}.Publish(ctx, version)

	if err != nil {
		return err
	}

	return nil
}
