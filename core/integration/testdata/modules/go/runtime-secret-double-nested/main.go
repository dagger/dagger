package main

import (
	"context"
	"fmt"
)

type Mymodule struct{}

func (m *Mymodule) Issue(ctx context.Context) error {
	kc := dag.Keychain()

	err := kc.Get(ctx, "a")
	if err != nil {
		return fmt.Errorf("first get: %w", err)
	}

	err = kc.Get(ctx, "a")
	if err != nil {
		return fmt.Errorf("second get, same args: %w", err)
	}

	err = kc.Get(ctx, "b")
	if err != nil {
		return fmt.Errorf("third get: %w", err)
	}
	return nil
}
