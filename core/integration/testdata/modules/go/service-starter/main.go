package main

import (
	"context"

	"dagger/starter/internal/dagger"
)

type Starter struct{}

func (m *Starter) Start(ctx context.Context, s *dagger.Service) {
	go func() { s.Up(ctx) }()
}
