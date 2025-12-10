// A module to develop, build, test Go softwares
package main

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	doublestar "github.com/bmatcuk/doublestar/v4"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/modules/go/internal/dagger"
	"github.com/dagger/dagger/modules/go/internal/telemetry"
	"github.com/dagger/dagger/util/parallel"
)

const (
	defaultPlatform = dagger.Platform("")
)

func New(
	// Project source directory
	// +defaultPath="/"
	source *dagger.Directory,
	// Go version
	// +optional
	// +default="1.25.5"
	version string,
	// Use a custom module cache
	// +optional
	moduleCache *dagger.CacheVolume,

	// Use a custom build cache
	// +optional
	buildCache *dagger.CacheVolume,

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

	// Enable go experiments https://pkg.go.dev/internal/goexperiment
	// +optional
	experiment []string,

	// extra system packages to include in the default base image; only
	// valid if 'base' arg is nil
	// +optional
	extraPackages []string,
) *Go {
	if source == nil {
		source = dag.Directory()
	}
	if moduleCache == nil {
		// Cache volumes should be namespaced by module, but they aren't (yet).
		// For now, we namespace them explicitly here.
		moduleCache = dag.CacheVolume("github.com/dagger/dagger/modules/go:modules")
	}
	if buildCache == nil {
		// Cache volumes should be namespaced by module, but they aren't (yet).
		// For now, we namespace them explicitly here.
		buildCache = dag.CacheVolume("github.com/dagger/dagger/modules/go:build")
	}
	if base == nil {
		packages := []string{
			"go~" + version,
			"ca-certificates",
			// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
			"build-base",
			// adding the git CLI to inject vcs info into the go binaries
			"git",
			// Install protoc for protobug support by default
			// The specific version is dictated by Dagger's own requirement
			// FIXME: make this optional with overlay support
			"protobuf~32", // ADD: brings /usr/bin/protoc and runtime libs
			"protobuf-dev~32",
		}
		if len(extraPackages) > 0 {
			packages = append(packages, extraPackages...)
		}
		base = dag.
			Wolfi().
			Container(dagger.WolfiContainerOpts{Packages: packages}).
			WithEnvVariable("GOLANG_VERSION", version).
			WithEnvVariable("GOPATH", "/go").
			WithEnvVariable("PATH", "${GOPATH}/bin:${PATH}", dagger.ContainerWithEnvVariableOpts{Expand: true}).
			WithDirectory("/usr/local/bin", dag.Directory()).
			// Configure caches
			WithMountedCache("/go/pkg/mod", moduleCache).
			WithMountedCache("/root/.cache/go-build", buildCache).
			WithWorkdir("/app")
	}
	return &Go{
		Version:     version,
		Source:      source,
		ModuleCache: moduleCache,
		BuildCache:  buildCache,
		Base:        base,
		Ldflags:     ldflags,
		Values:      values,
		Cgo:         cgo,
		Race:        race,
		Experiment:  experiment,
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

	// Pass arguments to 'go build -ldflags''
	Ldflags []string

	// Add string value definition of the form importpath.name=value
	Values []string

	// Enable CGO
	Cgo bool

	// Enable race detector
	Race bool

	// Enable go experiments
	Experiment []string

	Include []string

	Exclude []string
}

type AssociativeArray[T any] []struct {
	Key   string
	Value T
}

// Download dependencies into the module cache
// +cache="session"
func (p *Go) Download(ctx context.Context) (*Go, error) {
	_, err := p.Base.
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithDirectory("", p.Source, dagger.ContainerWithDirectoryOpts{
			Include: []string{"**/go.mod", "**/go.sum"},
		}).
		WithEnvVariable("CACHE_BUSTER", time.Now().Format("20060102-150405.000")).
		WithExec([]string{"go", "mod", "download"}).
		Sync(ctx)
	if err != nil {
		return p, err
	}
	return p, nil
}

// Prepare a build environment for the given Go source code:
//   - Build a base container with Go tooling installed and configured
//   - Apply configuration
//   - Mount the source code
func (p *Go) Env(
	// +optional
	platform dagger.Platform,
) *dagger.Container {
	return p.Base.
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
		// Configure experiments
		With(func(c *dagger.Container) *dagger.Container {
			if len(p.Experiment) == 0 {
				return c
			}
			return c.WithEnvVariable("GOEXPERIMENT", strings.Join(p.Experiment, ","))
		}).
		WithMountedDirectory("", p.Source)
}

// List tests
func (p *Go) Tests(
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

// Build the given main packages, and return the build directory
func (p *Go) Build(
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

	mainPkgs := pkgs
	hasWildcardPkgs := false
	for _, pkg := range pkgs {
		if strings.Contains(pkg, "...") {
			hasWildcardPkgs = true
			break
		}
	}
	if hasWildcardPkgs {
		var err error
		mainPkgs, err = p.ListPackages(ctx, pkgs, true)
		if err != nil {
			return nil, err
		}
	}

	ldflags := p.Ldflags
	if noSymbols {
		ldflags = append(ldflags, "-s")
	}
	if noDwarf {
		ldflags = append(ldflags, "-w")
	}

	env := p.Env(platform)
	cmd := []string{"go", "build", "-o", output}
	for _, pkg := range mainPkgs {
		env = env.WithExec(goCommand(cmd, []string{pkg}, ldflags, p.Values, p.Race))
	}
	return dag.Directory().WithDirectory(output, env.Directory(output)), nil
}

// Build a single main package, and return the compiled binary
func (p *Go) Binary(
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
		return nil, fmt.Errorf("no matching binary in %q", files)
	}
	return dir.File(files[0]), nil
}

// Run tests for the given packages
// +cache="session"
func (p *Go) Test(
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
	cmd = append(cmd, fmt.Sprintf("-count=%d", count))
	if run != "" {
		cmd = append(cmd, "-run", run)
	}
	if failfast {
		cmd = append(cmd, "-failfast")
	}
	if skip != "" {
		cmd = append(cmd, "-skip", skip)
	}
	_, err := p.
		Env(defaultPlatform).
		WithExec(goCommand(cmd, pkgs, p.Ldflags, p.Values, p.Race)).
		Sync(ctx)
	return err
}

// List packages matching the specified criteria
func (p *Go) ListPackages(
	ctx context.Context,
	// Filter by name or pattern. Example './foo/...'
	// +optional
	// +default=["./..."]
	pkgs []string,
	// Only list main packages
	// +optional
	onlyMain bool,
) ([]string, error) {
	args := []string{"go", "list"}
	if onlyMain {
		args = append(args, "-f", `{{if eq .Name "main"}}{{.Dir}}{{end}}`)
	} else {
		args = append(args, "-f", `{{.Dir}}`)
	}
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

func (p *Go) findParentDirs(ctx context.Context, dir *dagger.Directory, filename string, include []string, exclude []string) ([]string, []string, error) {
	entries, err := dir.Glob(ctx, "**/"+filename)
	if err != nil {
		return nil, nil, err
	}
	var dirs, skipped []string
	for _, entry := range entries {
		entry = filepath.Clean(entry)
		dir := strings.TrimSuffix(entry, "go.mod")
		if dir == "" {
			dir = "."
		}
		included, err := filterPath(dir, include, exclude)
		if err != nil {
			return dirs, skipped, err
		}
		if !included {
			skipped = append(skipped, dir)
			continue
		}
		dirs = append(dirs, dir)
	}
	return dirs, skipped, nil
}

// Scan the source for go modules, and return their paths
func (p *Go) Modules(
	ctx context.Context,
	include []string, // +optional
	exclude []string, // +optional
) ([]string, error) {
	mods, _, err := p.findParentDirs(ctx, p.Source, "go.mod", include, exclude)
	if err != nil {
		return nil, err
	}
	return mods, nil
}

func (p *Go) TidyModule(ctx context.Context, mod string) (*dagger.Changeset, error) {
	p, err := p.GenerateDaggerRuntime(ctx, mod)
	if err != nil {
		return nil, err
	}
	tidyModDir := p.Env(defaultPlatform).
		WithWorkdir(mod).
		WithExec([]string{"go", "mod", "tidy"}).
		Directory(".")
	return p.Source.
		WithFile(path.Join(mod, "/go.mod"), tidyModDir.File("go.mod")).
		WithFile(path.Join(mod, "/go.sum"), tidyModDir.File("go.sum")).
		Changes(p.Source), nil
}

func (p *Go) Tidy(
	ctx context.Context,
	include []string, // +optional
	exclude []string, // +optional
) (*dagger.Changeset, error) {
	modules, err := p.Modules(ctx, include, exclude)
	if err != nil {
		return nil, err
	}
	tidyModules := make([]*dagger.Changeset, len(modules))
	jobs := parallel.New()
	for i, mod := range modules {
		jobs = jobs.WithJob(mod, func(ctx context.Context) error {
			var err error
			tidyModules[i], err = p.TidyModule(ctx, mod)
			return err
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	return changesetMerge(tidyModules...), nil
}

// Merge Changesets together
// FIXME: move this to core dagger: https://github.com/dagger/dagger/issues/11189
// FIXME: this duplicates the same function in .dagger/util.go
// (cross-module function sharing is a PITA)
func changesetMerge(changesets ...*dagger.Changeset) *dagger.Changeset {
	before := dag.Directory()
	for _, changeset := range changesets {
		before = before.WithDirectory("", changeset.Before())
	}
	after := before
	for _, changeset := range changesets {
		after = after.WithChanges(changeset)
	}
	return after.Changes(before)
}

// Check if 'go mod tidy' is up-to-date
// +check
func (p *Go) CheckTidy(
	ctx context.Context,
	include []string, // +optional
	exclude []string, // +optional
) error {
	modules, err := p.Modules(ctx, include, exclude)
	if err != nil {
		return err
	}
	jobs := parallel.New().
		// On a large repo this can run dozens of parallel golangci-lint jobs,
		// which can lead to OOM or extreme CPU usage, so we limit parallelism
		WithLimit(3).
		// For better display in 'dagger checks': logs from all functions below the job will
		// be printed below the job.
		// TODO: remove this when dagger has a sub-checks API
		WithRollupLogs(true).
		// For better display in 'dagger checks': we get a cool activity bar in our sub-checks
		// TODO: remove this when dagger has a sub-checks API
		WithRollupSpans(true)
	for _, mod := range modules {
		jobs = jobs.WithJob(mod, func(ctx context.Context) error {
			diffTidy, err := p.TidyModule(ctx, mod)
			if err != nil {
				return err
			}
			changes, err := diffTidy.AsPatch().Contents(ctx)
			if err != nil {
				return err
			}
			if len(changes) > 0 {
				stdio := telemetry.SpanStdio(ctx, "")
				fmt.Fprint(stdio.Stderr, changes)
				return fmt.Errorf("%s: 'go mod tidy' must be run", mod)
			}
			return nil
		})
	}
	return jobs.Run(ctx)
}

func filterPath(path string, include, exclude []string) (bool, error) {
	if len(include) > 0 {
		for _, includePattern := range include {
			matchInclude, err := doublestar.PathMatch(includePattern, path)
			if err != nil {
				return false, err
			}
			if matchInclude {
				return true, nil
			}
		}
		return false, nil
	}
	if len(exclude) > 0 {
		for _, excludePattern := range exclude {
			matchExclude, err := doublestar.PathMatch(excludePattern, path)
			if err != nil {
				return false, err
			}
			if matchExclude {
				return false, nil
			}
		}
	}
	return true, nil
}

// Lint the project
// +check
func (p *Go) Lint(
	ctx context.Context,
	include []string, //+optional
	exclude []string, //+optional
) error {
	mods, err := p.Modules(ctx, include, exclude)
	if err != nil {
		return err
	}
	jobs := parallel.New().
		// On a large repo this can run dozens of parallel golangci-lint jobs,
		// which can lead to OOM or extreme CPU usage, so we limit parallelism
		WithLimit(3).
		// For better display in 'dagger checks': logs from all functions below the job will
		// be printed below the job.
		// TODO: remove this when dagger has a sub-checks API
		WithRollupLogs(true).
		// For better display in 'dagger checks': we get a cool activity bar in our sub-checks
		// TODO: remove this when dagger has a sub-checks API
		WithRollupSpans(true)
	for _, mod := range mods {
		jobs = jobs.WithJob(mod, func(ctx context.Context) error {
			return p.LintModule(ctx, mod)
		})
	}
	return jobs.Run(ctx)
}

func (p *Go) LintModule(ctx context.Context, mod string) error {
	lintImageRepo := "docker.io/golangci/golangci-lint"
	lintImageTag := "v2.5.0-alpine"
	lintImageDigest := "sha256:ac072ef3a8a6aa52c04630c68a7514e06be6f634d09d5975be60f2d53b484106"
	lintImage := lintImageRepo + ":" + lintImageTag + "@" + lintImageDigest
	p, err := p.GenerateDaggerRuntime(ctx, mod)
	if err != nil {
		return err
	}
	return parallel.Run(ctx, "lint", func(ctx context.Context) error {
		_, err = dag.Container().
			From(lintImage).
			WithMountedCache("/go/pkg/mod", p.ModuleCache).
			WithMountedCache("/root/.cache/go-build", p.BuildCache).
			WithMountedCache("/root/.cache/golangci-lint", dag.CacheVolume("golangci-lint")).
			WithWorkdir("/src").
			WithMountedDirectory(".", p.Source).
			WithWorkdir(mod).
			WithExec([]string{
				"golangci-lint", "run",
				"--path-prefix", mod + "/",
				"--output.tab.path=stderr",
				"--output.tab.print-linter-name=true",
				"--output.tab.colors=false",
				"--show-stats=false",
				"--max-issues-per-linter=0",
				"--max-same-issues=0",
			}).
			Sync(ctx)
		return err
	})
}

func (p *Go) GenerateDaggerRuntime(ctx context.Context, start string) (*Go, error) {
	var isInside bool
	var daggerModPath string
	parallel.Run(ctx, "check for dagger runtime", func(ctx context.Context) error {
		// 1. Are we in a dagger module?
		daggerJSONPath, err := p.Source.FindUp(ctx, "dagger.json", start)
		if err != nil {
			return err
		}
		if daggerJSONPath == "" {
			return nil
		}
		daggerJSONContents, err := p.Source.File(daggerJSONPath).Contents(ctx)
		if err != nil {
			return err
		}
		daggerJSON := dag.JSON().WithContents(dagger.JSON(daggerJSONContents))
		sdk, err := daggerJSON.Field([]string{"sdk", "source"}).AsString(ctx)
		if err != nil {
			// It's valid for a dagger.json to not have a source field
			return nil //nolint:nilerr
		}
		daggerModPath = path.Clean(strings.TrimSuffix(daggerJSONPath, "dagger.json"))

		// 2. Is the dagger module using the Go SDK?
		if sdk != "go" {
			return nil
		}
		// 3. Are we in the dagger module's *source* directory?
		sourceField, err := daggerJSON.Field([]string{"source"}).AsString(ctx)
		if err != nil {
			// If no source field, default to "."
			sourceField = "."
		}
		runtimeSourcePath := path.Clean(path.Join(daggerModPath, sourceField))
		rel, err := filepath.Rel(path.Clean("/"+runtimeSourcePath), path.Clean("/"+start))
		if err != nil {
			return err
		}
		isInside = !strings.HasPrefix(rel, "..")
		return nil
	})
	if isInside {
		if err := parallel.Run(ctx, "generate dagger runtime: "+daggerModPath, func(ctx context.Context) error {
			// 4. Match! Load the module and generate its files
			layer, err := p.Source.
				AsModule(dagger.DirectoryAsModuleOpts{SourceRootPath: daggerModPath}).
				GeneratedContextDirectory().Sync(ctx)
			if err != nil {
				return err
			}
			p.Source = p.Source.WithDirectory("", layer)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return p, nil
}
