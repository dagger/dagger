package main

import (
	"context"

	"dagger/generator-module/internal/dagger"
)

type GeneratorModule struct {
	// +private
	Password *dagger.Secret
}

func New() *GeneratorModule {
	return &GeneratorModule{
		Password: dag.SetSecret("pass", "admin"),
	}
}

func (m *GeneratorModule) Gen(ctx context.Context, name string) error {
	_, err := m.Password.Plaintext(ctx)
	return err
}
