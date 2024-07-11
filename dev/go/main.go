package main

import (
	"context"
	"path"

	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dev/go/internal/dagger"
)

func New(
	// Project source directory
	source *dagger.Directory,
	// Go version
	// +optional
	// +default="1.22.4"
	version string,
) *Go {
	if source == nil {
		source = dag.Directory()
	}
	return &Go{
		Version: version,
		Source:  source,
	}
}

// A Go project
type Go struct {
	// Go version
	Version string

	// Project source directory
	Source *dagger.Directory
}

// Build a base container with Go installed and configured
func (p *Go) Base() *dagger.Container {
	return dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{Packages: []string{
			"go~" + p.Version,
			// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
			"build-base",
			// adding the git CLI to inject vcs info
			// into the go binaries
			"git",
		}}).
		WithEnvVariable("GOLANG_VERSION", p.Version).
		WithEnvVariable("GOPATH", "/go").
		WithEnvVariable("PATH", "${GOPATH}/bin:${PATH}", dagger.ContainerWithEnvVariableOpts{Expand: true}).
		WithDirectory("/usr/local/bin", dag.Directory()).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		// include a cache for go build
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build"))
}

// Prepare a build environment for the given Go source code
//   - Build a base container with Go tooling installed and configured
//   - Mount the source code
//   - Download dependencies
func (p *Go) Env() *dagger.Container {
	return p.
		Base().
		WithEnvVariable("CGO_ENABLED", "0").
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithDirectory("/app", p.Source, dagger.ContainerWithDirectoryOpts{
			Include: []string{"**/go.mod", "**/go.sum"},
		}).
		WithExec([]string{"go", "mod", "download"}).
		// run `go build` with all source
		WithMountedDirectory("/app", p.Source)
}

// Lint the project
func (p *Go) Lint(
	ctx context.Context,

	pkgs []string, // +optional
	all bool, // +optional
) error {
	eg, ctx := errgroup.WithContext(ctx)
	for _, pkg := range pkgs {
		pkg := pkg
		eg.Go(func() error {
			ctx, span := Tracer().Start(ctx, "lint "+path.Clean(pkg))
			defer span.End()
			_, err := dag.
				Golangci().
				Lint(p.Source, GolangciLintOpts{Path: pkg}).
				Assert(ctx)
			return err
		})
		eg.Go(func() error {
			ctx, span := Tracer().Start(ctx, "tidy "+path.Clean(pkg))
			defer span.End()
			beforeTidy := p.Source.Directory(pkg)
			afterTidy := p.Env().WithWorkdir(pkg).WithExec([]string{"go", "mod", "tidy"}).Directory(".")
			// FIXME: the client binding for AssertEqual should return only an error.
			_, err := dag.Dirdiff().AssertEqual(ctx, beforeTidy, afterTidy, []string{"go.mod", "go.sum"})
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
			}
			return err
		})
	}
	return eg.Wait()
}
