package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

var script = `#!/bin/sh
echo "Test Suite"
echo "=========="
echo "Test 1: PASS" >> report.txt
echo "Test 2: FAIL" >> report.txt
echo "Test 3: PASS" >> report.txt
exit 1
`

func main() {
	if err := run(); err != nil {
		// Don't panic
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()

	return Test(ctx, client)
}

func Test(ctx context.Context, client *dagger.Client) error {
	// The result of `Sync` is the container, which allows continued chaining.
	ctr, err := client.
		Container().
		From("alpine").
		// Add script with execution permission to simulate a testing tool.
		WithNewFile("run-tests", dagger.ContainerWithNewFileOpts{
			Contents:    script,
			Permissions: 0o750,
		}).
		// If the exit code isn't needed: "run-tests; true"
		WithExec([]string{"sh", "-c", "/run-tests; echo -n $? > /exit_code"}).
		Sync(ctx)
	if err != nil {
		// Unexpected error, could be network failure.
		return fmt.Errorf("run tests: %w", err)
	}

	// Save report locally for inspection.
	_, err = ctr.
		File("report.txt").
		Export(ctx, "report.txt")
	if err != nil {
		// Test suite ran but there's no report file.
		return fmt.Errorf("get report: %w", err)
	}

	// Use the saved exit code to determine if the tests passed.
	exitCode, err := ctr.File("/exit_code").Contents(ctx)
	if err != nil {
		return fmt.Errorf("get exit code: %w", err)
	}

	if exitCode != "0" {
		fmt.Fprintln(os.Stderr, "Tests failed!")
	} else {
		fmt.Println("Tests passed!")
	}

	return nil
}
