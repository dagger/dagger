package main

import (
	"context"
	"dagger/engine-dev/internal/dagger"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// Run specific tests while curling (pprof) dumps from their associated dev engine:
// defaults to heap dumps, eg: take a heap dump every second and one after the tests complete:
// `dagger call test dump --run=TestCache/TestVolume --pkg=./core/integration --interval=1s export --path=/tmp/dump-$(date +"%Y%m%d_%H%M%S")`
// but also works for profiles:
// `dagger call test dump --run=TestCache/TestVolume --pkg=./core/integration --route=pprof/profile --no-final export --path=/tmp/dump-$(date +"%Y%m%d_%H%M%S")`
// +cache="session"
func (dev *EngineDev) TestDump(
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
	// How many tests to run in parallel - defaults to the number of CPUs
	// +optional
	parallel int,
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
	ctr, debugEndpoint, err := dev.testContainer(ctx, nil)
	if err != nil {
		return nil, err
	}
	ctr = dev.test(ctx, ctr, &testOpts{
		runTestRegex:  run,
		skipTestRegex: skip,
		pkg:           pkg,
		failfast:      failfast,
		parallel:      parallel,
		timeout:       timeout,
		race:          race,
		count:         count,
		update:        false,
		testVerbose:   testVerbose,
		bench:         false,
	})
	return dev.pprofDump(ctx, ctr, debugEndpoint, &dumpOpts{
		route:    route,
		noFinal:  noFinal,
		delay:    d,
		interval: i,
	})
}

// Dump pprof data from the given test container and service endpoint
func (dev *EngineDev) pprofDump(
	ctx context.Context,
	testContainer *dagger.Container,
	debugEndpoint string,
	opts *dumpOpts,
) (*dagger.Directory, error) {
	dumps := dag.Directory()
	baseFileName := strings.ReplaceAll(opts.route, "/", "-")
	dumpCount := 0 // not strictly necessary, but a nice sanity check and less faff than using dumps.Entries()

	cancelCtx, cancel := context.WithCancel(ctx)
	eg := errgroup.Group{}
	wait := opts.delay
	eg.Go(func() error {
		var dumpErr error
		for {
			select {
			case <-cancelCtx.Done():
				return dumpErr
			case <-time.After(wait):
				heapData, err := fetchDump(ctx, debugEndpoint, opts.route)
				dumpErr = errors.Join(dumpErr, err)
				if err == nil {
					fileName := fmt.Sprintf("%s-%d.pprof", baseFileName, dumpCount)
					dumps = dumps.WithFile(fileName, heapData)
					dumpCount++
				}
				if opts.interval < 0 {
					return dumpErr
				}
				wait = opts.interval
			}
		}
	})

	_, testErr := testContainer.Sync(ctx)
	cancel()
	dumpErr := eg.Wait()

	if !opts.noFinal {
		heapData, finalDumpErr := fetchDump(ctx, debugEndpoint, opts.route)
		dumpErr = errors.Join(dumpErr, finalDumpErr)
		if finalDumpErr == nil {
			fileName := fmt.Sprintf("%s-final.pprof", baseFileName)
			dumps = dumps.WithFile(fileName, heapData)
			dumpCount++
		}
	}

	if testErr != nil {
		if dumpCount == 0 {
			return nil, fmt.Errorf("no dumps collected and test failed: %w, %w", testErr, dumpErr)
		}
		return dumps, testErr
	}

	if dumpCount == 0 {
		return nil, fmt.Errorf("test passed, but no dumps collected: %w", dumpErr)
	}
	return dumps, nil
}

// fetchDump fetches from a debug HTTP endpoint and returns it as a dagger.File
func fetchDump(ctx context.Context, debugEndpoint string, route string) (*dagger.File, error) {
	url := fmt.Sprintf("%s/debug/%s", debugEndpoint, route)
	curlContainer := dag.Wolfi().Container(dagger.WolfiContainerOpts{Packages: []string{"curl"}}).
		WithExec([]string{
			"curl",
			"--fail",
			"--silent",
			"--show-error",
			"--max-time", "120", // Timeout after 120 seconds (for longer CPU profiles)
			"--output", "/dump",
			url,
		})

	exitCode, err := curlContainer.ExitCode(ctx)
	if err != nil {
		return nil, err
	}

	if exitCode != 0 {
		stderr, _ := curlContainer.Stderr(ctx)
		return nil, fmt.Errorf("failed to fetch dump, curl exit code: %d, stderr: %s", exitCode, stderr)
	}

	return curlContainer.File("/dump"), nil
}

type dumpOpts struct {
	route    string
	noFinal  bool
	delay    time.Duration
	interval time.Duration
}
