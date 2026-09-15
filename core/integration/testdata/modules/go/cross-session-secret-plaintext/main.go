package main

import (
	"context"
	"fmt"

	"dagger/secreter/internal/dagger"
)

type Secreter struct{}

func (*Secreter) CheckPlaintext(ctx context.Context, s *dagger.Secret, expected string) error {
	plaintext, err := s.Plaintext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get plaintext: %w", err)
	}
	if plaintext != expected {
		return fmt.Errorf("expected %q, got %q", expected, plaintext)
	}
	return nil
}
