package main

import (
	"context"
	"dagger/minimal/utils"
)

type Minimal struct{}

func (m *Minimal) Hello(ctx context.Context) (string, error) {
	return utils.Foo().File("foo").Contents(ctx)
}
