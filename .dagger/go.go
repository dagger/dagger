package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// GoToolchain toolchain
type GoToolchain struct {
	Go *dagger.Go // +private
}

// Go returns the Go toolchain
func (dev *DaggerDev) Go(
	ctx context.Context,
	// +defaultPath="/"
	// +ignore=[
	// "bin",
	// ".git",
	// "**/node_modules",
	// "**/.venv",
	// "**/__pycache__",
	// "docs/node_modules",
	// "sdk/typescript/node_modules",
	// "sdk/typescript/dist",
	// "sdk/rust/examples/backend/target",
	// "sdk/rust/target",
	// "sdk/php/vendor"
	// ]
	source *dagger.Directory,
) *GoToolchain {
	return &GoToolchain{dag.Go(dagger.GoOpts{Source: source})}
}

// An exclude filter for modules that are known to be broken, and should not be linted, checked or generated.
var knownBrokenModules = []string{
	"docs/**",
	"core/integration/**",
	"dagql/idtui/viztest/broken/**",
	"modules/evals/**",
	"**/broken*/**",
}

// Lint the Go codebase
// TODO: remove when go is installed as a toolchain
func (g *GoToolchain) Lint(ctx context.Context) error {
	return g.Go.Lint(ctx, dagger.GoLintOpts{
		Exclude: knownBrokenModules,
	})
}

// CheckTidy checks that go modules have up-to-date go.mod and go.sum
// TODO: remove when go is installed as a toolchain
func (g *GoToolchain) CheckTidy(ctx context.Context) error {
	return g.Go.CheckTidy(ctx, dagger.GoCheckTidyOpts{
		Exclude: knownBrokenModules,
	})
}

func (g *GoToolchain) Tidy() *dagger.Changeset {
	return g.Go.Tidy(dagger.GoTidyOpts{
		Exclude: knownBrokenModules,
	})
}
