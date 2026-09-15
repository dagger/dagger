package main

import (
	"context"
	"fmt"
)

type Toplevel struct{}

func (t *Toplevel) Attempt(ctx context.Context) error {
	secret := dag.SetSecret("FOO", "outer")
	_, err := secret.ID(ctx)
	if err != nil {
		return err
	}

	secret2 := dag.Maker().MakeSecret()

	plaintext, err := secret.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "outer" {
		return fmt.Errorf("expected \"outer\", but got %q", plaintext)
	}

	plaintext, err = secret2.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "inner" {
		return fmt.Errorf("expected \"inner\", but got %q", plaintext)
	}

	return nil
}
