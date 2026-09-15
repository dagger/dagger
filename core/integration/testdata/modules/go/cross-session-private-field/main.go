package main

import (
	"context"
	"fmt"

	"dagger/test/internal/dagger"
)

type Test struct{}

// Holder stores a credential object in a private field. The function result
// is cached across sessions by default function caching; the seed arg gives
// each test run a distinct cache identity.
func (*Test) Holder(seed string) *Holder {
	_ = seed
	return &Holder{Cred: dag.Cred().Login()}
}

type Holder struct {
	// +private
	Cred *dagger.Cred
}

// Use reads through the private field. The salt arg busts this function's
// cache so it re-executes against the stored Holder state.
func (h *Holder) Use(ctx context.Context, salt string) (string, error) {
	token, err := h.Cred.Show(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%s", salt, token), nil
}
