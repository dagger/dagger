package main

import (
	"context"
	"dagger/dep/internal/dagger"
	"fmt"
)

type Dep struct{}

func (m *Dep) Fn(ctx context.Context, ctr *dagger.Container) error {
	out, err := ctr.Stdout(ctx)
	if err != nil {
		return err
	}
	if out != "yoyoyo" {
		return fmt.Errorf("unexpected output: %s", out)
	}
	return nil
}
