package main

import (
	"context"
	"fmt"
)

type Toplevel struct{}

func (t *Toplevel) TryReturn(ctx context.Context) error {
	text, err := dag.Secreter().Make().Plaintext(ctx)
	if err != nil {
		return err
	}
	if text != "inner" {
		return fmt.Errorf("expected \"inner\", but got %q", text)
	}
	return nil
}

func (t *Toplevel) TryArg(ctx context.Context) error {
	text, err := dag.Secreter().Get(ctx, dag.SetSecret("BAR", "outer"))
	if err != nil {
		return err
	}
	if text != "outer" {
		return fmt.Errorf("expected \"outer\", but got %q", text)
	}
	return nil
}
