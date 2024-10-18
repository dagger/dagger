package main

import (
	"context"
	"path"

	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/modules/go/internal/dagger"
)

func New(
	// Project source directory
	source *dagger.Directory,
	// Go version
	// +optional
	// +default="1.23.2"
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
func (p *Go) Base(
	// Any extra apk packages that should be included in the base image
	// +optional
	extraPackages []string,
) *dagger.Container {
	pkgs := []string{
		"go~" + p.Version,
		// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
		"build-base",
		// adding the git CLI to inject vcs info into the go binaries
		"git",
	}
	pkgs = append(pkgs, extraPackages...)
	return dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{Packages: pkgs}).
		WithEnvVariable("GOLANG_VERSION", p.Version).
		WithEnvVariable("GOPATH", "/go").
		WithEnvVariable("PATH", "${GOPATH}/bin:${PATH}", dagger.ContainerWithEnvVariableOpts{Expand: true}).
		WithDirectory("/usr/local/bin", dag.Directory()).
		WithMountedCache("/go/pkg/mod", p.goModCache()).
		WithMountedCache("/root/.cache/go-build", p.goBuildCache())
}

// Prepare a build environment for the given Go source code
//   - Build a base container with Go tooling installed and configured
//   - Mount the source code
//   - Download dependencies
func (p *Go) Env(
	// Any extra apk packages that should be included in the base image
	// +optional
	extraPackages []string,
) *dagger.Container {
	return p.
		Base(extraPackages).
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
	packages []string, // +optional
) error {
	eg, ctx := errgroup.WithContext(ctx)
	for _, pkg := range packages {
		eg.Go(func() error {
			ctx, span := Tracer().Start(ctx, "lint "+path.Clean(pkg))
			defer span.End()
			return dag.
				Golangci().
				Lint(p.Source, dagger.GolangciLintOpts{
					Path:         pkg,
					GoModCache:   p.goModCache(),
					GoBuildCache: p.goBuildCache(),
				}).
				Assert(ctx)
		})
		eg.Go(func() error {
			ctx, span := Tracer().Start(ctx, "tidy "+path.Clean(pkg))
			defer span.End()
			beforeTidy := p.Source.Directory(pkg)
			afterTidy := p.Env(nil).WithWorkdir(pkg).WithExec([]string{"go", "mod", "tidy"}).Directory(".")
			err := dag.Dirdiff().AssertEqual(ctx, beforeTidy, afterTidy, []string{"go.mod", "go.sum"})
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
			}
			return err
		})
	}
	return eg.Wait()
}

func (p *Go) goModCache() *dagger.CacheVolume {
	return dag.CacheVolume("go-mod")
}

func (p *Go) goBuildCache() *dagger.CacheVolume {
	return dag.CacheVolume("go-build")
}
