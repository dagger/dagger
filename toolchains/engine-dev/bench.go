package main

import (
	"context"
	"fmt"
	"time"

	"dagger/engine-dev/internal/dagger"
)

// Perform a benchmark of the given test run
// +cache="session"
func (dev *EngineDev) Benchmark(
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
	// notify this discord webhook on failure
	// +optional
	discordWebhook *dagger.Secret,
	// Git repository to extract git metadata for discord notification
	// +defaultPath="/"
	repo *dagger.GitRepository,
) error {
	return dev.notifyOnFailure(ctx, repo, dev.bench(
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
	), discordWebhook)
}

func (dev *EngineDev) notifyOnFailure(ctx context.Context, repo *dagger.GitRepository, err error, discordWebhook *dagger.Secret) error {
	if err == nil {
		return nil
	}
	if discordWebhook == nil {
		return err
	}

	commit, err := repo.Head().Commit(ctx)
	if err != nil {
		commit = "failed to find commit SHA"
	}

	daggerCloudURL, err := dag.Notify().DaggerCloudTraceURL(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch trace URL for failed benchmarks: %w", err)
	}

	message := fmt.Sprintf(
		"[failed](%s) on SHA [%s](https://github.com/dagger/dagger/commit/%s)",
		daggerCloudURL,
		commit,
		commit,
	)
	_, discordErr := dag.Notify().Discord(ctx, discordWebhook, message)
	if discordErr != nil {
		return fmt.Errorf("failed to notify discord that benchmarks failed: %w, discord error %w", err, discordErr)
	}
	return err
}

// Run specific benchmarks while curling (pprof) dumps from their associated dev engine:
// defaults to heap dumps, eg: take a heap dump every second and one after the tests complete:
// `dagger call test dump --run=TestCache/TestVolume --pkg=./core/integration --interval=1s export --path=/tmp/dump-$(date +"%Y%m%d_%H%M%S")`
// but also works for profiles:
// `dagger call test dump --run=TestCache/TestVolume --pkg=./core/integration --route=pprof/profile --no-final export --path=/tmp/dump-$(date +"%Y%m%d_%H%M%S")`
// +cache="session"
func (dev *EngineDev) BenchmarkDump(
	ctx context.Context,
	// Only run these tests
	// +optional
	run string,
	// Skip these tests
	// +optional
	skip string,
	// +optional
	// +default="./..."
	pkg string,
	// Abort test run on first failure
	// +optional
	failfast bool,
	// How long before timing out the test run
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
	// debug subroute to dump, like pprof/profile, pprof/heap, or requests
	// +optional
	// +default="pprof/heap"
	route string,
	// when set, don't take a final dump after the tests have completed. usually good with --route="pprof/profile".
	// +optional'
	// +default=false
	noFinal bool,
	// wait this long before starting to take dumps. delay does not include engine startup.
	// +optional
	// +default="1s"
	delay string,
	// wait this long between dumps. negative values will fetch exactly 1 dump excluding the one controlled by "final"
	// +optional
	// +default="-1s"
	interval string,
) (*dagger.Directory, error) {
	d, err := time.ParseDuration(delay)
	if err != nil {
		return nil, err
	}
	i, err := time.ParseDuration(interval)
	if err != nil {
		return nil, err
	}
	return dev.benchDump(
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
		},
		&dumpOpts{
			route:    route,
			noFinal:  noFinal,
			delay:    d,
			interval: i,
		},
	)
}

func (dev *EngineDev) benchDump(
	ctx context.Context,
	opts *benchOpts,
	dOpts *dumpOpts,
) (*dagger.Directory, error) {
	ctr, debugEndpoint, err := dev.testContainer(ctx, nil)
	if err != nil {
		return nil, err
	}
	ctr = dev.test(ctx, ctr, &testOpts{
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
	})
	return dev.pprofDump(ctx, ctr, debugEndpoint, dOpts)
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

func (dev *EngineDev) bench(
	ctx context.Context,
	opts *benchOpts,
) error {
	run := func(cmd *dagger.Container) *dagger.Container {
		return dev.test(ctx, cmd, &testOpts{
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

	ctr, _, err := dev.testContainer(ctx, nil)
	if err != nil {
		return err
	}

	if opts.prewarm {
		_, err = run(ctr.WithEnvVariable("TESTCTX_PREWARM", "true")).Sync(ctx)
		if err != nil {
			return fmt.Errorf("failed during prewarm run: %w", err)
		}
	}
	_, err = run(ctr).Sync(ctx)

	return err
}
