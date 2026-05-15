package main

import (
	"context"
	"encoding/base64"

	"dagger/dep/internal/dagger"
)

type Dep struct{}

func (m *Dep) Bar(
	ctx context.Context,
	// +optional
	s *dagger.Secret,
) (string, error) {
	pt, err := s.Plaintext(ctx)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(pt)), nil
}
