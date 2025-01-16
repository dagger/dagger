package main

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

type Bench struct {
	Test *Test // +private
}

func (b *Bench) All(
	ctx context.Context,
	// +optional
	failfast bool,
	// +optional
	timeout string,
	// +optional
	race bool,
	// +optional
	testVerbose bool,
	// run benchmarks once with metrics tagged "prewarm" before running for real
	// +optional
	prewarm bool,
) error {
	return b.bench(
		ctx,
		&benchOpts{
			runTestRegex:  "",
			skipTestRegex: "",
			pkg:           "./...",
			failfast:      failfast,
			timeout:       timeout,
			race:          race,
			count:         1,
			testVerbose:   testVerbose,
			prewarm:       prewarm,
		},
	)
}

func (b *Bench) Specific(
	ctx context.Context,
	// Only run these benchmarks
	// +optional
	run string,
	// Skip these benchmarks
	// +optional
	skip string,
	// +optional
	// +default="./..."
	pkg string,
	// Abort bench run on first failure
	// +optional
	failfast bool,
	// How long before timing out the benchmark run
	// +optional
	timeout string,
	// +optional
	race bool,
	// +default=1
	// +optional
	count int,
	// Enable verbose output
	// +optional
	testVerbose bool,
	// run benchmarks once with metrics tagged "prewarm" before running for real
	// +optional
	prewarm bool,
) error {
	return b.bench(
		ctx,
		&benchOpts{
			runTestRegex:  run,
			skipTestRegex: skip,
			pkg:           pkg,
			failfast:      failfast,
			timeout:       timeout,
			race:          race,
			count:         count,
			testVerbose:   testVerbose,
			prewarm:       prewarm,
		},
	)
}

type benchOpts struct {
	runTestRegex  string
	skipTestRegex string
	pkg           string
	failfast      bool
	timeout       string
	race          bool
	count         int
	testVerbose   bool
	prewarm       bool
}

func (b *Bench) bench(
	ctx context.Context,
	opts *benchOpts,
) error {
	run := func(cmdBase *dagger.Container) *dagger.Container {
		return b.Test.goTest(
			cmdBase,
			&goTestOpts{
				runTestRegex:  opts.runTestRegex,
				skipTestRegex: opts.skipTestRegex,
				pkg:           opts.pkg,
				failfast:      opts.failfast,
				parallel:      0,
				timeout:       opts.timeout,
				race:          opts.race,
				count:         opts.count,
				update:        false,
				testVerbose:   opts.testVerbose,
				bench:         true,
			},
		)
	}

	cmd, err := b.Test.testCmd(ctx)
	if err != nil {
		return err
	}

	if opts.prewarm {
		_, err = run(cmd.WithEnvVariable("TESTCTX_PREWARM", "true")).
			Sync(ctx)
		if err != nil {
			return fmt.Errorf("failed during prewarm run: %w", err)
		}
	}

	_, err = run(cmd).Sync(ctx)
	return err
}
