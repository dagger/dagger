package main

import (
	"context"
	"fmt"
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

// Handle errors
func (m *MyModule) Test(ctx context.Context) (string, error) {
	ctr, err := dag.
		Container().
		From("alpine").
		// add script with execution permission to simulate a testing tool
		WithNewFile("run-tests", dagger.ContainerWithNewFileOpts{
			Contents:    script,
			Permissions: 0o750,
		}).
		// if the exit code isn't needed: "run-tests; true"
		WithExec([]string{"sh", "-c", "/run-tests; echo -n $? > /exit_code"}).
		// the result of `sync` is the container, which allows continued chaining
		Sync(ctx)
	if err != nil {
		// unexpected error, could be network failure.
		return "", fmt.Errorf("run tests: %w", err)
	}
	// save report locally for inspection.
	_, err = ctr.
		File("report.txt").
		Export(ctx, "report.txt")
	if err != nil {
		// test suite ran but there's no report file.
		return "", fmt.Errorf("get report: %w", err)
	}

	// use the saved exit code to determine if the tests passed.
	exitCode, err := ctr.File("/exit_code").Contents(ctx)
	if err != nil {
		// exit code not found
		return "", fmt.Errorf("get exit code: %w", err)
	}

	if exitCode != "0" {
		return "Tests failed!", nil
	} else {
		return "Tests passed!", nil
	}

}
