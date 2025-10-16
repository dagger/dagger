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
) (*GoToolchain, error) {
	v := dag.Version()
	version, err := v.Version(ctx)
	if err != nil {
		return nil, err
	}
	tag, err := v.ImageTag(ctx)
	if err != nil {
		return nil, err
	}
	g := &GoToolchain{
		Go: dag.Go(dagger.GoOpts{
			Source: source,
			Values: []string{
				"github.com/dagger/dagger/engine.Version=" + version,
				"github.com/dagger/dagger/engine.Tag=" + tag,
			},
		},
		),
	}
	return g, nil
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
func (g *GoToolchain) Lint(ctx context.Context) (MyCheckStatus, error) {
	_, err := g.Go.Lint(ctx, dagger.GoLintOpts{
		Exclude: knownBrokenModules,
	})
	return CheckCompleted, err
}

// CheckTidy checks that go modules have up-to-date go.mod and go.sum
// TODO: remove when go is installed as a toolchain
func (g *GoToolchain) CheckTidy(ctx context.Context) (MyCheckStatus, error) {
	_, err := g.Go.CheckTidy(ctx, dagger.GoCheckTidyOpts{
		Exclude: knownBrokenModules,
	})
	return CheckCompleted, err
}

func (g *GoToolchain) Tidy() *dagger.Changeset {
	return g.Go.Tidy(dagger.GoTidyOpts{
		Exclude: knownBrokenModules,
	})
}

func (g *GoToolchain) Env() *dagger.Container {
	return g.Go.Env()
}
