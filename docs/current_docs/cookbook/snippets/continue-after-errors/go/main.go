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
echo "Test 1: PASS" >> report.txt
echo "Test 2: FAIL" >> report.txt
echo "Test 3: PASS" >> report.txt
exit 1
`

type TestResult struct {
	Report   *dagger.File
	ExitCode string
}

// Handle errors
func (m *MyModule) Test(ctx context.Context) (*TestResult, error) {
	ctr, err := dag.
		Container().
		From("alpine").
		// add script with execution permission to simulate a testing tool
		WithNewFile("run-tests", script, dagger.ContainerWithNewFileOpts{Permissions: 0o750}).
		// if the exit code isn't needed: "run-tests; true"
		WithExec([]string{"sh", "-c", "/run-tests; echo -n $? > /exit_code"}).
		// the result of `sync` is the container, which allows continued chaining
		Sync(ctx)
	if err != nil {
		// unexpected error, could be network failure.
		return nil, fmt.Errorf("run tests: %w", err)
	}
	// save report for inspection.
	report := ctr.File("report.txt")

	// use the saved exit code to determine if the tests passed.
	exitCode, err := ctr.File("/exit_code").Contents(ctx)
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
