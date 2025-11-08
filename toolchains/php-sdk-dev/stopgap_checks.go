package main

import (
	"context"

	"github.com/dagger/dagger/util/parallel"
)

// Lint the PHP SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (t PhpSdkDev) Lint(ctx context.Context) error {
	return parallel.
		New().
		WithJob("PHP CodeSniffer", t.PhpCodeSniffer).
		WithJob("PHPStan", t.PhpStan).
		Run(ctx)
}
