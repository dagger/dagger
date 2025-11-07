package main

import (
	"context"

	"github.com/dagger/dagger/util/parallel"
)

// This file contains temporary code, to be removed once 'dagger checks' is merged and released.
type MyCheckStatus string

const (
	CheckCompleted MyCheckStatus = "COMPLETED"
	CheckSkipped   MyCheckStatus = "SKIPPED"
)

// Lint the PHP SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (t PhpSdkDev) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("PHP CodeSniffer", func(ctx context.Context) error {
			_, err := t.PhpCodeSniffer(ctx)
			return err
		}).
		WithJob("PHPStan", func(ctx context.Context) error {
			_, err := t.PhpStan(ctx)
			return err
		}).
		Run(ctx)
}
