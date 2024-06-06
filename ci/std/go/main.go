package main

import (
	"context"

	"golang.org/x/sync/errgroup"
)

func New(
	// Project source directory
	source *Directory,
	// Go version
	// +optional
	// +default="1.22.3"
	version string,
) *Go {
	if source == nil {
		source = dag.Directory()
	}
	return &Go{Version: version, Source: source}
}

// A Go project
type Go struct {
	// Go version
	Version string
	// Project source directory
	Source *Directory
}

// Build a base container with Go installed and configured
func (p *Go) Base() *Container {
	return dag.
		Wolfi().
		Container(WolfiContainerOpts{Packages: []string{
			"go~" + p.Version,
			// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
			"build-base",
			// adding the git CLI to inject vcs info
			// into the go binaries
			"git",
		}}).
		WithEnvVariable("GOLANG_VERSION", p.Version).
		WithEnvVariable("GOPATH", "/go").
		WithEnvVariable("PATH", "${GOPATH}/bin:${PATH}", ContainerWithEnvVariableOpts{Expand: true}).
		WithDirectory("/usr/local/bin", dag.Directory()).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		// include a cache for go build
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build"))
}

// Prepare a build environment for the given Go source code
//   - Build a base container with Go tooling installed and configured
//   - Mount the source code
//   - Download dependencies
func (p *Go) Env() *Container {
	return p.
		Base().
		WithEnvVariable("CGO_ENABLED", "0").
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithDirectory("/app", p.Source, ContainerWithDirectoryOpts{
			Include: []string{"**/go.mod", "**/go.sum"},
		}).
		WithExec([]string{"go", "mod", "download"}).
		// run `go build` with all source
		WithMountedDirectory("/app", p.Source)
}

// Lint the project
func (p *Go) Lint(
	ctx context.Context,

	pkgs []string,
	all bool, // +optional
) error {
	eg, ctx := errgroup.WithContext(ctx)

	cmd := []string{"golangci-lint", "run", "-v", "--timeout", "5m"}
	if all {
		cmd = append(cmd, "--max-issues-per-linter=0", "--max-same-issues=0")
	}
	// FIXME: consider using the same base container in Lint() and Env()
	base := dag.
		Container().
		From("golangci/golangci-lint:v1.57-alpine").
		WithMountedDirectory("/app", p.Source).
		WithWorkdir("/app")
	for _, pkg := range pkgs {
		pkg := pkg
		golangci := base.WithWorkdir(pkg).WithExec(cmd)
		eg.Go(func() error {
			_, err := golangci.Sync(ctx)
			return err
		})
		eg.Go(func() error {
			beforeTidy := p.Source.Directory(pkg)
			afterTidy := p.Env().WithWorkdir(pkg).WithExec([]string{"go", "mod", "tidy"}).Directory(".")
			// FIXME: the client binding for AssertEqual should return only an error.
			_, err := dag.Dirdiff().AssertEqual(ctx, beforeTidy, afterTidy, []string{"go.mod", "go.sum"})
			return err
		})
	}
	return eg.Wait()
}
