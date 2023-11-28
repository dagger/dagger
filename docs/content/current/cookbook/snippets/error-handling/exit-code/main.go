package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"dagger.io/dagger"
)

// WarningExit is the exit code for warnings.
const WarningExit = 5

var reportCmd = `
echo "QA Checks"
echo "========="
echo "Check 1: PASS"
echo "Check 2: FAIL"
echo "Check 3: PASS"
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
		return fmt.Errorf("dagger connect: %w", err)
	}
	defer client.Close()

	err = Test(ctx, client)
	if err != nil {
		// Unexpected error (not from WithExec).
		return fmt.Errorf("test pipeline: %w", err)
	}

	result, err := Report(ctx, client)
	if err != nil {
		// Unexpected error (not from WithExec).
		return fmt.Errorf("report pipeline: %w", err)
	}
	fmt.Println(result)

	return nil
}

func Test(ctx context.Context, client *dagger.Client) error {
	_, err := client.
		Container().
		From("alpine").
		WithExec([]string{"sh", "-c", "echo Skipped! >&2; exit 5"}).
		Sync(ctx)

		// Handle error from WithExec error here, but let other errors bubble up.
	var e *dagger.ExecError
	if errors.As(err, &e) {
		// Don't do anything when skipped.
		// Print message to stderr otherwise.
		if e.ExitCode != WarningExit {
			fmt.Fprintf(os.Stderr, "Test failed: %s", e.Stderr)
		}
		return nil
	}
	return err
}

func Report(ctx context.Context, client *dagger.Client) (string, error) {
	output, err := client.
		Container().
		From("alpines"). // ⚠️ typo! non-exec failure
		WithExec([]string{"sh", "-c", reportCmd}).
		Stdout(ctx)

	// Get stdout even on non-zero exit.
	var e *dagger.ExecError
	if errors.As(err, &e) {
		// Not necessary to check for `e.ExitCode != 0`.
		return e.Stdout, nil
	}
	return output, err
}
