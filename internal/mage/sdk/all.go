package sdk

import (
	"context"

	"github.com/magefile/mage/mg"
)

type SDK interface {
	Lint(context.Context) error
	Test(context.Context) error
	Generate(context.Context) error
}

var availableSDKs = []SDK{
	&Go{},
}

var _ SDK = All{}

type All mg.Namespace

// Lint runs all SDK linters
func (t All) Lint(ctx context.Context) error {
	return t.runAll(func(s SDK) any {
		return s.Lint
	})
}

// Test runs all SDK tests
func (t All) Test(ctx context.Context) error {
	return t.runAll(func(s SDK) any {
		return s.Test
	})
}

// Generate re-generates all SDK APIs
func (t All) Generate(ctx context.Context) error {
	return t.runAll(func(s SDK) any {
		return s.Generate
	})
}

func (t All) runAll(fn func(SDK) any) error {
	handlers := []any{}
	for _, sdk := range availableSDKs {
		handlers = append(handlers, fn(sdk))
	}
	mg.Deps(handlers...)
	return nil
}
