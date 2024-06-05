package main

import (
	"context"
	"errors"
	"fmt"
)

type MyModule struct{}

// Generate an error
func (m *MyModule) Test(ctx context.Context) (string, error) {
	out, err := dag.
		Container().
		From("alpine").
		// ERROR: cat: read error: Is a directory
		WithExec([]string{"cat", "/"}).
		Stdout(ctx)
	var e *ExecError
	if errors.As(err, &e) {
		return fmt.Sprintf("Test pipeline failure: %s", e.Stderr), nil
	} else if err != nil {
		return "", err
	}
	return out, nil
}
