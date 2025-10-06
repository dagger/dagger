package main

import (
	"context"
	"fmt"
)

func main() {
	dag.CurrentEnvironment().
		WithCheck(DummyCheck).
		Serve()
}

func DummyCheck(ctx context.Context) error {
	return fmt.Errorf("🤡")
}
