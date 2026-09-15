package main

import (
	"context"
	"fmt"

	"dagger/secreter/internal/dagger"
)

type Secreter struct{}

func (*Secreter) CheckEmptyPlaintext(ctx context.Context, s *dagger.Secret) error {
	plaintext, err := s.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "" {
		return fmt.Errorf("expected empty plaintext, got %q", plaintext)
	}
	return nil
}
