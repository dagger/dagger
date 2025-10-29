package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	doublestar "github.com/bmatcuk/doublestar/v4"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/modules/go/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

const (
	defaultPlatform = dagger.Platform("")
)

func New(
	// Project source directory
	source *dagger.Directory,
	// Go version
	// +optional
	// +default="1.25.2"
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

	// +optional
	include []string,

	// +optional
	exclude []string,
) Go {
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
		base = dag.
			Wolfi().
			Container(dagger.WolfiContainerOpts{Packages: []string{
				"go~" + version,
				// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
				"build-base",
				// adding the git CLI to inject vcs info into the go binaries
				"git",
				// Install protoc for protobug support by default
				// The specific version is dictated by Dagger's own requirement
				// FIXME: make this optional with overlay support
				"protobuf~32", // ADD: brings /usr/bin/protoc and runtime libs
				"protobuf-dev~32",
				"ca-certificates",
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
		Ldflags:     ldflags,
		Values:      values,
		Cgo:         cgo,
		Race:        race,
		Experiment:  experiment,
		Include:     include,
		Exclude:     exclude,
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

func (p Go) GenerateDaggerRuntimes(ctx context.Context) (Go, error) {
	mods, err := p.Modules(ctx)
	if err != nil {
		return p, err
	}
	daggerMods := map[string]*dagger.Directory{}
	if err := parallel.Run(ctx, "scan for dagger runtimes to generate", func(ctx context.Context) error {
		for _, mod := range mods {
			daggerJSONPath, err := p.Source.FindUp(ctx, "dagger.json", mod)
			if err != nil {
				return err
			}
			if daggerJSONPath == "" {
				continue
			}
			daggerJSONContents, err := p.Source.File(daggerJSONPath).Contents(ctx)
			if err != nil {
				return err
			}
			sdk, err := dag.JSON().WithContents(dagger.JSON(daggerJSONContents)).Field([]string{"sdk", "source"}).AsString(ctx)
			if err != nil {
				// sdk field might just not exist - skip (FIXME)
				continue
			}
			fmt.Fprintf(os.Stderr, "[%s] sdk=%s\n", mod, sdk)
			if sdk != "go" {
				continue
			}
			daggerModPath := path.Clean(strings.TrimSuffix(daggerJSONPath, "dagger.json"))
			daggerMods[daggerModPath] = nil
		}
		return nil
	}); err != nil {
		return p, err
	}
	if len(daggerMods) == 0 {
		return p, nil
	}
	var daggerModsMu sync.Mutex
	if err := parallel.Run(ctx,
		fmt.Sprintf("generate %d dagger runtimes", len(daggerMods)),
		func(ctx context.Context) error {
			jobs := parallel.New().WithLimit(3)
			for daggerMod := range daggerMods {
				jobs = jobs.WithJob(daggerMod, func(ctx context.Context) error {
					layer := p.Source.
						AsModule(dagger.DirectoryAsModuleOpts{SourceRootPath: daggerMod}).
						GeneratedContextDirectory().
						Directory(daggerMod)
					result, err := dag.Directory().WithDirectory(daggerMod, layer).Sync(ctx)
					if err != nil {
						return err
					}
					daggerModsMu.Lock()
					daggerMods[daggerMod] = result
					daggerModsMu.Unlock()
					return nil
				},
				)
			}
			return jobs.Run(ctx)
		}); err != nil {
		return p, err
	}
	err = parallel.Run(ctx, "merge generated dagger runtimes", func(ctx context.Context) error {
		for _, genRuntime := range daggerMods {
			if genRuntime == nil {
				continue
			}
			p.Source = p.Source.WithDirectory("", genRuntime)
		}
		var err error
		p.Source, err = p.Source.Sync(ctx)
		return err
	})
	return p, err
}

// Download dependencies into the module cache
func (p Go) Download(ctx context.Context) (Go, error) {
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
func (p Go) Env(
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

// Build the given main packages, and return the build directory
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
		return nil, fmt.Errorf("no matching binary in %q", files)
	}
	return dir.File(files[0]), nil
}

// Run tests for the given packages
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

func (p Go) findParentDirs(ctx context.Context, dir *dagger.Directory, filename string) ([]string, []string, error) {
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
		included, err := filterPath(dir, p.Include, p.Exclude)
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
func (p Go) Modules(ctx context.Context) ([]string, error) {
	mods, _, err := p.findParentDirs(ctx, p.Source, "go.mod")
	if err != nil {
		return nil, err
	}
	return mods, nil
}

func (p Go) TidyModule(path string) *dagger.Changeset {
	tidyModDir := p.Env(defaultPlatform).
		WithWorkdir(path).
		WithExec([]string{"go", "mod", "tidy"}).
		Directory(".")
	return p.Source.
		WithFile(path+"/go.mod", tidyModDir.File("go.mod")).
		WithFile(path+"/go.sum", tidyModDir.File("go.sum")).
		Changes(p.Source)
}

// Check if 'go mod tidy' is up-to-date
func (p Go) CheckTidy(ctx context.Context) error {
	p, err := p.GenerateDaggerRuntimes(ctx)
	if err != nil {
		return err
	}
	modules, err := p.Modules(ctx)
	if err != nil {
		return err
	}
	jobs := parallel.New()
	for _, mod := range modules {
		jobs = jobs.WithJob(mod, func(ctx context.Context) error {
			diffSize, err := p.TidyModule(mod).AsPatch().Size(ctx)
			if err != nil {
				return err
			}
			if diffSize > 0 {
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
func (p Go) CheckLint(ctx context.Context) error {
	p, err := p.GenerateDaggerRuntimes(ctx)
	if err != nil {
		return err
	}
	lintImageRepo := "docker.io/golangci/golangci-lint"
	lintImageTag := "v2.5.0-alpine"
	lintImageDigest := "sha256:ac072ef3a8a6aa52c04630c68a7514e06be6f634d09d5975be60f2d53b484106"
	lintImage := lintImageRepo + ":" + lintImageTag + "@" + lintImageDigest
	configPath := "/etc/golangci.yml"
	modules, err := p.Modules(ctx)
	if err != nil {
		return err
	}
	// On a large repo this can run dozens of parallel golangci-lint jobs,
	// which can lead to OOM or extreme CPU usage, so we limit parallelism
	jobs := parallel.New().WithLimit(3)
	for _, mod := range modules {
		jobs = jobs.WithJob(mod, func(ctx context.Context) error {
			_, err := dag.Container().
				From(lintImage).
				WithFile(configPath, dag.CurrentModule().Source().File("lint-config.yml")).
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
					"--config", configPath,
				}).
				Sync(ctx)
			return err
		})
	}
	return jobs.Run(ctx)
}
