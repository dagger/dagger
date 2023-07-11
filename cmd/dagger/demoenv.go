package main

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/dagger/universe"
)

type DemoEnv struct {
	Checks    []DemoCheck
	Artifacts []DemoArtifact
}

var ProgrockEnv = DemoEnv{
	Checks: []DemoCheck{
		{
			Name:        "test",
			Description: "run tests",
			Func:        Test,
		},
		{
			Name:        "lint",
			Description: "run linters",
			Func:        Lint,
		},
		{
			Name:        "demo",
			Description: "make sure the demo builds",
			Func: func(ctx universe.Context) (string, error) {
				dir, err := BuildDemo(ctx)
				if err != nil {
					return "", err
				}
				_, err = dir.Entries(ctx)
				return "ok", err
			},
		},
	},
	Artifacts: []DemoArtifact{
		{
			Name:        "demo",
			Description: "demo binary that shows off Progrock's TUI",
			Func:        BuildDemo,
		},
		{
			Name:        "generate",
			Description: "go generate ./...",
			Func:        Generate,
		},
	},
}

// dagger.Context equivalent
type Context struct {
	context.Context
	client *dagger.Client
}

var _ universe.Context = Context{}

func (ctx Context) Client() *dagger.Client {
	return ctx.client
}

type DemoCheck struct {
	Name        string
	Description string
	Func        func(universe.Context) (string, error)
}

type DemoArtifact struct {
	Name        string
	Description string
	Func        func(universe.Context) (*dagger.Directory, error)
}

func BuildDemo(ctx universe.Context) (*dagger.Directory, error) {
	return universe.GoBuild(ctx, Base(ctx), Code(ctx), universe.GoBuildOpts{
		Packages: []string{"./demo"},
		Static:   true,
		Subdir:   "demo",
	}), nil
}

func Generate(ctx universe.Context) (*dagger.Directory, error) {
	return universe.GoGenerate(ctx, Base(ctx), Code(ctx)), nil
}

func Test(ctx universe.Context) (string, error) {
	return universe.Gotestsum(ctx, Base(ctx), Code(ctx)).Stdout(ctx)
}

func Lint(ctx universe.Context) (string, error) {
	return universe.GolangCILint(ctx, Base(ctx), Code(ctx)).Stdout(ctx)
}

func Base(ctx universe.Context) *dagger.Container {
	return universe.Wolfi(ctx, []string{
		"go",
		"golangci-lint",
		"protobuf-dev", // for google/protobuf/*.proto
		"protoc",
		"protoc-gen-go",
		"protoc-gen-go-grpc",
	}).
		With(universe.GoBin).
		WithExec([]string{"go", "install", "gotest.tools/gotestsum@latest"})
}

func Code(ctx universe.Context) *dagger.Directory {
	return ctx.Client().Host().Directory(".", dagger.HostDirectoryOpts{
		Include: []string{
			"**/*.go",
			"**/go.mod",
			"**/go.sum",
			"**/testdata/**/*",
			"**/*.proto",
			"**/*.tmpl",
		},
		Exclude: []string{
			"ci/**/*",
		},
	})
}
