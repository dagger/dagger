// A module to develop, build, test Go softwares
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	doublestar "github.com/bmatcuk/doublestar/v4"
	telemetry "github.com/dagger/otel-go"
	toml "github.com/pelletier/go-toml"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/modules/go/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

const (
	defaultPlatform        = dagger.Platform("")
	moduleConfigFilename   = "dagger-module.toml"
	legacyModuleConfigFile = "dagger.json"
)

func New(
	ctx context.Context,

	// Project source directory
	// +defaultPath="/"
	source *dagger.Directory,
	// Go version
	// +optional
	// +default="1.26"
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

	// Pass arguments to 'go build -ldflags'
	// +optional
	ldflags []string,

	// Pass arguments to 'go build -tags'
	// +optional
	tags []string,

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

	// max number of parallel jobs to run for tidy/check tidy/lint/runtime generation
	// +default=10
	limit int,

	// Workspace whose git HEAD commit and dirty state are stamped into built
	// binaries as VCS info (see the stamping block in New).
	//
	// The engine only auto-injects a Workspace on a *direct* client call;
	// module-runtime callers don't inherit it. Rather than forward the
	// Workspace (a session-scoped resource that would taint this build's cache
	// key and break disk-cache reuse across engine restarts), parent
	// toolchains resolve it to the scalar vcsCommit/vcsDirty below, which take
	// precedence over ws. Omitted → no stamping.
	//
	// +optional
	ws *dagger.Workspace,

	// Resolved VCS commit to stamp, forwarded by a parent toolchain. Takes
	// precedence over ws so the Workspace never enters this build's cache key.
	// +optional
	vcsCommit string,

	// Resolved VCS dirty state to stamp, paired with vcsCommit.
	// +optional
	vcsDirty bool,
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
			"go-" + version,
			"ca-certificates",
			// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
			"build-base",
			// adding the git CLI to inject vcs info into the go binaries
			"git",
			// Install protoc for protobug support by default
			// The specific version is dictated by Dagger's own requirement
			// FIXME: make this optional with overlay support
			"protobuf~35", // ADD: brings /usr/bin/protoc and runtime libs
			"protobuf-dev~35",
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
	// Stamp git commit / dirty state into built binaries as VCS info, via
	// -ldflags into the Dagger buildinfo package's Injected* vars. Prefer the
	// scalars a parent toolchain resolved for us; otherwise resolve the
	// auto-injected workspace (a direct call). Errors are swallowed — the
	// build proceeds with whatever we collected. Only the resolved strings are
	// ever stamped, never the Workspace itself, so the build stays
	// content-addressed and its result survives an engine restart.
	if vcsCommit == "" && ws != nil {
		git := ws.Git()
		if commit, err := git.Head().Commit(ctx); err == nil {
			vcsCommit = commit
			if clean, err := git.Uncommitted().IsEmpty(ctx); err == nil {
				vcsDirty = !clean
			}
		}
	}
	if vcsCommit != "" {
		values = append(values,
			"github.com/dagger/dagger/internal/version/buildinfo.InjectedVCS=git",
			"github.com/dagger/dagger/internal/version/buildinfo.InjectedVCSRevision="+vcsCommit,
			"github.com/dagger/dagger/internal/version/buildinfo.InjectedVCSModified="+strconv.FormatBool(vcsDirty),
		)
		// TODO: also inject InjectedVCSTime once a Workspace.git commit-time
		// accessor is available.
	}
	return &Go{
		Version:     version,
		Source:      source,
		ModuleCache: moduleCache,
		BuildCache:  buildCache,
		Base:        base,
		Ldflags:     ldflags,
		Tags:        tags,
		Values:      values,
		Cgo:         cgo,
		Race:        race,
		Experiment:  experiment,
		Limit:       limit,
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

	// Pass arguments to 'go build -ldflags'
	Ldflags []string

	// Pass arguments to 'go build -tags'
	Tags []string

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

	// Max number of parallel jobs to run
	Limit int
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
		env = env.WithExec(goCommand(cmd, []string{pkg}, ldflags, p.Tags, p.Values, p.Race))
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
		WithExec(goCommand(cmd, pkgs, p.Ldflags, p.Tags, p.Values, p.Race)).
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
	tags []string,
	values []string,
	race bool,
) []string {
	for _, val := range values {
		ldflags = append(ldflags, "-X '"+val+"'")
	}
	if len(ldflags) > 0 {
		cmd = append(cmd, "-ldflags", strings.Join(ldflags, " "))
	}
	if len(tags) > 0 {
		cmd = append(cmd, "-tags", strings.Join(tags, ","))
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

func (p *Go) TidyModule(module string) (*dagger.Changeset, error) {
	tidyModDir := p.Env(defaultPlatform).
		WithWorkdir(module).
		WithExec([]string{"go", "mod", "tidy"}).
		Directory(".")
	return p.Source.
		WithFile(path.Join(module, "/go.mod"), tidyModDir.File("go.mod")).
		WithFile(path.Join(module, "/go.sum"), tidyModDir.File("go.sum")).
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
	jobs := parallel.New().
		// On a large repo this can run dozens of parallel go mod tidy jobs,
		// which can lead to OOM or extreme CPU usage, so we limit parallelism
		WithLimit(p.Limit)
	for i, mod := range modules {
		jobs = jobs.WithJob(mod, func(ctx context.Context) error {
			var err error
			tidyModules[i], err = p.TidyModule(mod)
			return err
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	return dag.Changeset().WithChangesets(tidyModules), nil
}

// Generate Dagger runtime files for Go SDK modules in the configured source.
// +generate
func (p *Go) GenerateDaggerRuntimes(ctx context.Context) (*dagger.Changeset, error) {
	before := p.Source

	modules, err := p.Modules(ctx, nil, nil)
	if err != nil {
		return nil, err
	}

	layers := make([]*dagger.Directory, len(modules))
	jobs := parallel.New().
		WithLimit(p.Limit)
	for i, module := range modules {
		i, module := i, module
		jobs = jobs.WithJob(module, func(ctx context.Context) error {
			layer, ok, err := p.generateDaggerRuntimeLayer(ctx, module)
			if err != nil {
				return err
			}
			if ok {
				layers[i] = layer
			}
			return nil
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}

	after := before
	for _, layer := range layers {
		if layer != nil {
			after = after.WithDirectory("", layer)
		}
	}
	diff := after.Changes(before)
	contents, err := diff.AsPatch().Contents(ctx)
	if err != nil {
		return nil, err
	}
	fmt.Println("vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv")
	fmt.Print(contents)
	return diff, nil
}

func (p *Go) GenerateDaggerRuntime(ctx context.Context, start string) (*Go, error) {
	layer, ok, err := p.generateDaggerRuntimeLayer(ctx, start)
	if err != nil {
		return nil, err
	}
	if ok {
		p.Source = p.Source.WithDirectory("", layer)
	}
	return p, nil
}

func (p *Go) generateDaggerRuntimeLayer(ctx context.Context, start string) (*dagger.Directory, bool, error) {
	var isInside bool
	var daggerModPath string
	if err := parallel.Run(ctx, "check for dagger runtime", func(ctx context.Context) error {
		// 1. Are we in a dagger module?
		daggerConfigPath, err := p.findUpModuleConfig(ctx, start)
		if err != nil {
			return err
		}
		if daggerConfigPath == "" {
			return nil
		}
		daggerConfigContents, err := p.Source.File(daggerConfigPath).Contents(ctx)
		if err != nil {
			return err
		}
		daggerConfig, err := parseModuleConfigSummary([]byte(daggerConfigContents), path.Base(daggerConfigPath))
		if err != nil {
			return err
		}
		if daggerConfig.Runtime.Source == "" {
			// It's valid for a module config to not have a runtime/source field.
			return nil
		}
		daggerModPath = path.Clean(strings.TrimSuffix(daggerConfigPath, path.Base(daggerConfigPath)))

		// 2. Is the dagger module using the Go SDK?
		if daggerConfig.Runtime.Source != "go" {
			return nil
		}
		// 3. Are we in the dagger module's *source* directory?
		sourceField := daggerConfig.Source
		if sourceField == "" {
			sourceField = "."
		}
		runtimeSourcePath := path.Clean(path.Join(daggerModPath, sourceField))
		rel, err := filepath.Rel(path.Clean("/"+runtimeSourcePath), path.Clean("/"+start))
		if err != nil {
			return err
		}
		isInside = !strings.HasPrefix(rel, "..")
		return nil
	}); err != nil {
		return nil, false, err
	}
	if !isInside {
		return nil, false, nil
	}

	var layer *dagger.Directory
	if err := parallel.Run(ctx, "generate dagger runtime: "+daggerModPath, func(ctx context.Context) error {
		// 4. Match! Load the module and generate its files
		var err error
		layer, err = p.Source.
			AsModule(dagger.DirectoryAsModuleOpts{SourceRootPath: daggerModPath}).
			GeneratedContextDirectory().Sync(ctx)
		return err
	}); err != nil {
		return nil, false, err
	}
	return layer, true, nil
}

func (p *Go) findUpModuleConfig(ctx context.Context, start string) (configPath string, err error) {
	currentPath, err := p.Source.FindUp(ctx, moduleConfigFilename, start)
	if err != nil {
		return "", err
	}
	legacyPath, err := p.Source.FindUp(ctx, legacyModuleConfigFile, start)
	if err != nil {
		return "", err
	}

	switch {
	case currentPath == "" && legacyPath == "":
		return "", nil
	case legacyPath == "":
		return currentPath, nil
	case currentPath == "":
		return legacyPath, nil
	case moduleConfigPathDepth(legacyPath) > moduleConfigPathDepth(currentPath):
		return legacyPath, nil
	default:
		return currentPath, nil
	}
}

// The Go SDK only needs enough config to decide whether it should generate a
// runtime layer. Keep this local so the SDK module does not import core/modules
// and pull the engine dependency graph into its own go.mod.
type moduleConfigSummary struct {
	Runtime moduleConfigRuntime
	Source  string
}

type moduleConfigRuntime struct {
	Source string `json:"source" toml:"source"`
}

func (runtime *moduleConfigRuntime) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if data[0] == '"' {
		return json.Unmarshal(data, &runtime.Source)
	}
	type alias moduleConfigRuntime
	return json.Unmarshal(data, (*alias)(runtime))
}

func parseModuleConfigSummary(data []byte, filename string) (*moduleConfigSummary, error) {
	if filename == legacyModuleConfigFile {
		var cfg struct {
			SDK    moduleConfigRuntime `json:"sdk"`
			Source string              `json:"source"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		return &moduleConfigSummary{
			Runtime: cfg.SDK,
			Source:  cfg.Source,
		}, nil
	}

	var cfg struct {
		Runtime moduleConfigRuntime `toml:"runtime"`
		Source  string              `toml:"source"`
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &moduleConfigSummary{
		Runtime: cfg.Runtime,
		Source:  cfg.Source,
	}, nil
}

func moduleConfigPathDepth(configPath string) int {
	dir := path.Dir(configPath)
	if dir == "." || dir == "/" {
		return 0
	}
	return strings.Count(strings.Trim(dir, "/"), "/") + 1
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
		// On a large repo this can run dozens of parallel go mod tidy jobs,
		// which can lead to OOM or extreme CPU usage, so we limit parallelism
		WithLimit(p.Limit).
		// For better display in 'dagger checks': logs from all functions below the job will
		// be printed below the job.
		// TODO: remove this when dagger has a sub-checks API
		WithRollupLogs(true).
		// For better display in 'dagger checks': we get a cool activity bar in our sub-checks
		// TODO: remove this when dagger has a sub-checks API
		WithRollupSpans(true)
	for _, mod := range modules {
		jobs = jobs.WithJob(mod, func(ctx context.Context) error {
			diffTidy, err := p.TidyModule(mod)
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
