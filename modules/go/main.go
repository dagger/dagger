package main

import (
	"context"
	"fmt"
	"path"
	"strings"

	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/modules/go/internal/dagger"
)

const (
	defaultPlatform = dagger.Platform("")
)

func New(
	// Project source directory
	source *dagger.Directory,
	// Go version
	// +optional
	// +default="1.23.0"
	version string,
	// Use a custom module cache
	// +optional
	moduleCache *dagger.CacheVolume,

	// Use a custom build cache
	// +optional
	buildCache *dagger.CacheVolume,

	// Attach an optional sidecar to the go environment
	// +optional
	sidecar Sidecar,

	// Use a custom base container.
	// The container must have Go installed.
	// +optional
	base *dagger.Container,

	// Pass arguments to 'go build -ldflags''
	// +optional
	ldflags []string,

	// Add string value definition of the form importpath.name=value
	// Example: "github.com/my/module.Foo=bar"
	// +optional
	values []string,

	// Enable CGO
	// +optional
	cgo bool,

	// Enable race detector. Implies cgo=true
	// +optional
	race bool,
) Go {
	if source == nil {
		source = dag.Directory()
	}
	if moduleCache == nil {
		moduleCache = dag.CacheVolume("github.com/dagger/dagger/modules/go:modules")
	}
	if buildCache == nil {
		buildCache = dag.CacheVolume("github.com/dagger/dagger/modules/go:build")
	}
	if base == nil {
		base = dag.
			Wolfi().
			Container(dagger.WolfiContainerOpts{Packages: []string{
				"go~" + version,
				// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
				"build-base",
				// adding the git CLI to inject vcs info into the go binaries
				"git",
			}}).
			WithEnvVariable("GOLANG_VERSION", version).
			WithEnvVariable("GOPATH", "/go").
			WithEnvVariable("PATH", "${GOPATH}/bin:${PATH}", dagger.ContainerWithEnvVariableOpts{Expand: true}).
			WithDirectory("/usr/local/bin", dag.Directory()).
			// Configure caches
			WithMountedCache("/go/pkg/mod", moduleCache).
			WithMountedCache("/root/.cache/go-build", buildCache).
			WithWorkdir("/app")
	}
	return Go{
		Version:     version,
		Source:      source,
		ModuleCache: moduleCache,
		BuildCache:  buildCache,
		Base:        base,
		Sidecar:     sidecar,
		Ldflags:     ldflags,
		Values:      values,
		Cgo:         cgo,
		Race:        race,
	}
}

// A Go project
type Go struct {
	// Go version
	Version string

	// Project source directory
	Source *dagger.Directory

	// Go module cache
	ModuleCache *dagger.CacheVolume

	// Go build cache
	BuildCache *dagger.CacheVolume

	// Base container from which to run all operations
	Base *dagger.Container

	Sidecar Sidecar // +private

	// Pass arguments to 'go build -ldflags''
	Ldflags []string

	// Add string value definition of the form importpath.name=value
	Values []string

	// Enable CGO
	Cgo bool

	// Enable race detector
	Race bool
}

type Sidecar interface {
	dagger.DaggerObject
	Bind(c *dagger.Container) *dagger.Container
}

// Download dependencies into the module cache
func (p Go) Download() Go {
	p.Base = p.Base.
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithDirectory("", p.Source, dagger.ContainerWithDirectoryOpts{
			Include: []string{"**/go.mod", "**/go.sum"},
		}).
		WithExec([]string{"go", "mod", "download"})
	return p
}

// Prepare a build environment for the given Go source code
//   - Build a base container with Go tooling installed and configured
//   - Mount the source code
//   - Download dependencies
func (p Go) Env(
	// +optional
	platform dagger.Platform,
) *dagger.Container {
	return p.Base.
		// Attach sidecar
		With(func(c *dagger.Container) *dagger.Container {
			if p.Sidecar != nil {
				return p.Sidecar.Bind(c)
			}
			return c
		}).
		// Configure CGO
		WithEnvVariable("CGO_ENABLED", func() string {
			if p.Cgo {
				return "1"
			}
			return "0"
		}()).
		// Configure platform
		With(func(c *dagger.Container) *dagger.Container {
			if platform == "" {
				return c
			}
			spec := platforms.Normalize(platforms.MustParse(string(platform)))
			c = c.
				WithEnvVariable("GOOS", spec.OS).
				WithEnvVariable("GOARCH", spec.Architecture)
			switch spec.Architecture {
			case "arm", "arm64":
				switch spec.Variant {
				case "", "v8":
				default:
					c = c.WithEnvVariable("GOARM", strings.TrimPrefix(spec.Variant, "v"))
				}
			}
			return c
		}).
		WithMountedDirectory("", p.Source)
}

// List tests
func (p Go) Tests(
	ctx context.Context,
	// Packages to list tests from (default all packages)
	// +optional
	// +default=["./..."]
	pkgs []string,
) (string, error) {
	script := "go test -list=. " + strings.Join(pkgs, " ") + " | grep ^Test | sort"
	return p.
		Env(defaultPlatform).
		WithExec([]string{"sh", "-c", script}).
		Stdout(ctx)
}

func (p Go) Build(
	ctx context.Context,
	// Which targets to build (default all main packages)
	// +optional
	// +default=["./..."]
	pkgs []string,
	// Disable symbol table
	// +optional
	noSymbols bool,
	// Disable DWARF generation
	// +optional
	noDwarf bool,
	// Target build platform
	// +optional
	platform dagger.Platform,
	// Output directory
	// +optional
	// +default="./bin/"
	output string,
) (*dagger.Directory, error) {
	if p.Race {
		p.Cgo = true
	}
	mainPkgs, err := p.ListPackages(ctx, pkgs, true)
	if err != nil {
		return nil, err
	}
	ldflags := p.Ldflags
	if noSymbols {
		ldflags = append(ldflags, "-s")
	}
	if noDwarf {
		ldflags = append(ldflags, "-w")
	}
	env := p.
		Download().
		Env(platform)
	cmd := []string{"go", "build", "-o", output}
	for _, pkg := range mainPkgs {
		env = env.WithExec(goCommand(cmd, []string{pkg}, ldflags, p.Values, p.Race))
	}
	return dag.Directory().WithDirectory(output, env.Directory(output)), nil
}

// Build a main package, and return the compiled binary
func (p Go) Binary(
	ctx context.Context,
	// Which package to build
	pkg string,
	// Disable symbol table
	// +optional
	noSymbols bool,
	// Disable DWARF generation
	// +optional
	noDwarf bool,
	// Target build platform
	// +optional
	platform dagger.Platform,
) (*dagger.File, error) {
	dir, err := p.Build(
		ctx,
		[]string{pkg},
		noSymbols,
		noDwarf,
		platform,
		"./bin/",
	)
	if err != nil {
		return nil, err
	}
	// The binary might be called dagger or dagger.exe
	files, err := dir.Glob(ctx, "bin/"+path.Base(pkg)+"*")
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("No matching binary in %q", files)
	}
	return dir.File(files[0]), nil
}

func (p Go) Test(
	ctx context.Context,
	// Only run these tests
	// +optional
	run string,
	// Skip these tests
	// +optional
	skip string,
	// Abort test run on first failure
	// +optional
	failfast bool,
	// How many tests to run in parallel - defaults to the number of CPUs
	// +optional
	// +default=0
	parallel int,
	// How long before timing out the test run
	// +optional
	// +default="30m"
	timeout string,
	// +optional
	// +default=1
	count int,
	// Which packages to test
	// +optional
	// +default=["./..."]
	pkgs []string,
) error {
	if p.Race {
		p.Cgo = true
	}
	cmd := []string{"go", "test", "-v"}
	if parallel != 0 {
		cmd = append(cmd, fmt.Sprintf("-parallel=%d", parallel))
	}
	cmd = append(cmd, fmt.Sprintf("-timeout=%s", timeout))
	if run != "" {
		cmd = append(cmd, "-run", run)
	}
	if skip != "" {
		cmd = append(cmd, "-skip", skip)
	}
	_, err := p.
		Download().
		Env(defaultPlatform).
		WithExec(goCommand(cmd, pkgs, p.Ldflags, p.Values, p.Race)).
		Sync(ctx)
	return err
}

// List packages matching the specified critera
func (p Go) ListPackages(
	ctx context.Context,
	// Filter by name or pattern. Example './foo/...'
	// +optional
	// +default=["./..."]
	pkgs []string,
	// Only list main packages
	// +optional
	onlyMain bool,
) ([]string, error) {
	args := []string{"go", "list", "-f", `{{if eq .Name "main"}}{{.Dir}}{{end}}`}
	args = append(args, pkgs...)
	out, err := p.Env(defaultPlatform).WithExec(args).Stdout(ctx)
	if err != nil {
		return nil, err
	}
	result := strings.Split(strings.Trim(out, "\n"), "\n")
	for i := range result {
		result[i] = strings.Replace(result[i], "/app/", "./", 1)
	}
	return result, nil
}

func goCommand(
	cmd []string,
	pkgs []string,
	ldflags []string,
	values []string,
	race bool,
) []string {
	for _, val := range values {
		ldflags = append(ldflags, "-X '"+val+"'")
	}
	if len(ldflags) > 0 {
		cmd = append(cmd, "-ldflags", strings.Join(ldflags, " "))
	}
	if race {
		cmd = append(cmd, "-race")
	}
	cmd = append(cmd, pkgs...)
	return cmd
}

// Lint the project
func (p Go) Lint(
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
				Lint(p.Source, dagger.GolangciLintOpts{Path: pkg}).
				Assert(ctx)
		})
		eg.Go(func() error {
			ctx, span := Tracer().Start(ctx, "tidy "+path.Clean(pkg))
			defer span.End()
			beforeTidy := p.Source.Directory(pkg)
			afterTidy := p.Env(defaultPlatform).WithWorkdir(pkg).WithExec([]string{"go", "mod", "tidy"}).Directory(".")
			err := dag.Dirdiff().AssertEqual(ctx, beforeTidy, afterTidy, []string{"go.mod", "go.sum"})
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
			}
			return err
		})
	}
	return eg.Wait()
}
