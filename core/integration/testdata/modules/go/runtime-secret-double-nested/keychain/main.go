package main

import "context"

type Keychain struct{}

func (m *Keychain) Get(ctx context.Context, name string) error {
	return dag.GeneratorModule().Gen(ctx, name)
}
