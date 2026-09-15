package main

import (
	"context"
	"fmt"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

var script = `#!/bin/sh
echo "Test Suite"
echo "=========="
echo "Test 1: PASS" | tee -a report.txt
echo "Test 2: FAIL" | tee -a report.txt
echo "Test 3: PASS" | tee -a report.txt
exit 1
`

type TestResult struct {
	Report   *dagger.File
	ExitCode int
}

// Handle errors
func (m *MyModule) Test(ctx context.Context) (*TestResult, error) {
	ctr, err := dag.
		Container().
		From("alpine").
		// add script with execution permission to simulate a testing tool
		WithNewFile("/run-tests", script, dagger.ContainerWithNewFileOpts{Permissions: 0o750}).
		// run-tests but allow any return code
		WithExec([]string{"/run-tests"}, dagger.ContainerWithExecOpts{Expect: dagger.Any}).
		// the result of `sync` is the container, which allows continued chaining
		Sync(ctx)
	if err != nil {
		// unexpected error, could be network failure.
		return nil, fmt.Errorf("run tests: %w", err)
	}
	// save report for inspection.
	report := ctr.File("report.txt")

	// use the saved exit code to determine if the tests passed.
	exitCode, err := ctr.ExitCode(ctx)
	if err != nil {
		// exit code not found
		return nil, fmt.Errorf("get exit code: %w", err)
	}

	// Return custom type
	return &TestResult{
		Report:   report,
		ExitCode: exitCode,
	}, nil
}
