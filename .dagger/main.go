// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/.dagger/internal/dagger"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct {
	Src     *dagger.Directory // +private
	Version *VersionInfo
	Tag     string

	// When set, module codegen is automatically applied when retrieving the Dagger source code
	ModCodegen        bool
	ModCodegenTargets []string

	// Can be used by nested clients to forward docker credentials to avoid
	// rate limits
	DockerCfg *dagger.Secret // +private

	// +private
	GitRef string
	GitDir *dagger.Directory
}

func New(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	// +ignore=["bin", ".git", "**/node_modules", "**/.venv", "**/__pycache__"]
	source *dagger.Directory,

	// Git directory, for metadata introspection
	// +optional
	// +defaultPath="/"
	// +ignore=["!.git"]
	gitDir *dagger.Directory,

	// +optional
	version string,
	// +optional
	tag string,

	// +optional
	dockerCfg *dagger.Secret,

	// Git ref (used for test-publish checks)
	// +optional
	ref string,
) (*DaggerDev, error) {
	versionInfo, err := newVersion(ctx, source, version)
	if err != nil {
		return nil, err
	}

	dev := &DaggerDev{
		Src:       source,
		Version:   versionInfo,
		Tag:       tag,
		DockerCfg: dockerCfg,
		GitRef:    ref,
		GitDir:    gitDir,
	}

	modules, err := dev.containing(ctx, "dagger.json")
	if err != nil {
		return nil, err
	}
	for _, module := range modules {
		if strings.HasPrefix(module, "docs/") {
			continue
		}
		if strings.HasPrefix(module, "core/integration/") {
			continue
		}
		dev.ModCodegenTargets = append(dev.ModCodegenTargets, module)
	}

	return dev, nil
}

// Enable module auto-codegen when retrieving the dagger source code
func (dev *DaggerDev) WithModCodegen() *DaggerDev {
	clone := *dev
	clone.ModCodegen = true
	return &clone
}

type Check func(context.Context) error

// Wrap 3 SDK-specific checks into a single check
type SDKChecks interface {
	Lint(ctx context.Context) error
	Test(ctx context.Context) error
	TestPublish(ctx context.Context, tag string) error
}

func (dev *DaggerDev) Ref(ctx context.Context) (string, error) {
	// Said .git introspection logic:
	ref, err := dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{Packages: []string{"git"}}).
		WithMountedDirectory("/src", dev.GitDir).
		WithWorkdir("/src").
		WithMountedFile("/bin/get-ref.sh", dag.CurrentModule().Source().File("get-ref.sh")).
		WithExec([]string{"sh", "/bin/get-ref.sh"}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	ref = strings.TrimRight(ref, "\n")
	fmt.Printf("git ref: from $GITHUB_REF='%s', from .git='%s'\n", dev.GitRef, ref)
	// FIXME: this shouldn't be needed.
	//  but at the moment it is, because introspection from .git
	//  doesn't work with TestPublish() for some reason.
	if (dev.GitRef != "") && (ref != dev.GitRef) {
		return dev.GitRef, nil
	}
	return ref, nil
}

func (dev *DaggerDev) sdkCheck(sdk string) Check {
	var checks SDKChecks
	switch sdk {
	case "python":
		checks = &PythonSDK{Dagger: dev}
	case "go":
		checks = NewGoSDK(dev.Source(), dev.Engine())
	case "typescript":
		checks = &TypescriptSDK{Dagger: dev}
	case "php":
		checks = &PHPSDK{Dagger: dev}
	case "java":
		checks = &JavaSDK{Dagger: dev}
	case "rust":
		checks = &RustSDK{Dagger: dev}
	case "elixir":
		checks = &ElixirSDK{Dagger: dev}
	}
	return func(ctx context.Context) (rerr error) {
		lint := func() (rerr error) {
			ctx, span := Tracer().Start(ctx, fmt.Sprintf("lint sdk/%s", sdk))
			defer func() {
				if rerr != nil {
					span.SetStatus(codes.Error, rerr.Error())
				}
				span.End()
			}()
			return checks.Lint(ctx)
		}
		test := func() (rerr error) {
			ctx, span := Tracer().Start(ctx, fmt.Sprintf("test sdk/%s", sdk))
			defer func() {
				if rerr != nil {
					span.SetStatus(codes.Error, rerr.Error())
				}
				span.End()
			}()
			return checks.Test(ctx)
		}
		testPublish := func() (rerr error) {
			ctx, span := Tracer().Start(ctx, fmt.Sprintf("test-publish sdk/%s", sdk))
			defer func() {
				if rerr != nil {
					span.SetStatus(codes.Error, rerr.Error())
				}
				span.End()
			}()
			// Inspect .git to avoid dependencing on $GITHUB_REF
			ref, err := dev.Ref(ctx)
			if err != nil {
				return fmt.Errorf("failed to introspect git ref: %s", err.Error())
			}
			fmt.Printf("===> ref = \"%s\"\n", ref)
			return checks.TestPublish(ctx, ref)
		}
		if err := lint(); err != nil {
			return err
		}
		if err := test(); err != nil {
			return err
		}
		if err := testPublish(); err != nil {
			return err
		}
		return nil
	}
}

const (
	CheckDocs          = "docs"
	CheckPythonSDK     = "sdk/python"
	CheckGoSDK         = "sdk/go"
	CheckTypescriptSDK = "sdk/typescript"
	CheckPHPSDK        = "sdk/php"
	CheckJavaSDK       = "sdk/java"
	CheckRustSDK       = "sdk/rust"
	CheckElixirSDK     = "sdk/elixir"
)

// Check that everything works. Use this as CI entrypoint.
func (dev *DaggerDev) Check(ctx context.Context,
	// Directories to check
	// +optional
	targets []string,
) error {
	var routes = map[string]Check{
		CheckDocs:          (&Docs{Dagger: dev}).Lint,
		CheckPythonSDK:     dev.sdkCheck("python"),
		CheckGoSDK:         dev.sdkCheck("go"),
		CheckTypescriptSDK: dev.sdkCheck("typescript"),
		CheckPHPSDK:        dev.sdkCheck("php"),
		CheckJavaSDK:       dev.sdkCheck("java"),
		CheckRustSDK:       dev.sdkCheck("rust"),
		CheckElixirSDK:     dev.sdkCheck("elixir"),
	}
	if len(targets) == 0 {
		targets = make([]string, 0, len(routes))
		for key := range routes {
			targets = append(targets, key)
		}
	}
	for _, target := range targets {
		if _, exists := routes[target]; !exists {
			return fmt.Errorf("no such target: %s", target)
		}
	}
	eg, ctx := errgroup.WithContext(ctx)
	for _, target := range targets {
		check := routes[target]
		eg.Go(func() error { return check(ctx) })
	}
	return eg.Wait()
}

// Develop the Dagger CLI
func (dev *DaggerDev) CLI() *CLI {
	return &CLI{Dagger: dev}
}

// Return the Dagger source code
func (dev *DaggerDev) Source() *dagger.Directory {
	if !dev.ModCodegen {
		return dev.Src
	}

	src := dev.Src
	for _, module := range dev.ModCodegenTargets {
		layer := dev.Src.
			AsModule(dagger.DirectoryAsModuleOpts{
				SourceRootPath: module,
			}).
			GeneratedContextDirectory().
			Directory(module)
		src = src.WithDirectory(module, layer)
	}
	return src
}

func (dev *DaggerDev) containing(ctx context.Context, filename string) ([]string, error) {
	entries, err := dev.Src.Glob(ctx, "**/"+filename)
	if err != nil {
		return nil, err
	}

	var parents []string
	for _, entry := range entries {
		entry = filepath.Clean(entry)
		parent := strings.TrimSuffix(entry, filename)
		if parent == "" {
			parent = "."
		}
		parents = append(parents, parent)
	}

	return parents, nil
}

// Dagger's Go toolchain
func (dev *DaggerDev) Go() *GoToolchain {
	return &GoToolchain{Go: dag.Go(dev.Source())}
}

type GoToolchain struct {
	// +private
	*dagger.Go
}

func (gtc *GoToolchain) Env() *dagger.Container {
	return gtc.Go.Env()
}

func (gtc *GoToolchain) Lint(
	ctx context.Context,
	packages []string,
) error {
	return gtc.Go.Lint(ctx, dagger.GoLintOpts{Packages: packages})
}

// Develop the Dagger engine container
func (dev *DaggerDev) Engine() *Engine {
	return &Engine{Dagger: dev}
}

// Develop the Dagger documentation
func (dev *DaggerDev) Docs() *Docs {
	return &Docs{Dagger: dev}
}

// Run Dagger scripts
func (dev *DaggerDev) Scripts() *Scripts {
	return &Scripts{Dagger: dev}
}

// Run all tests
func (dev *DaggerDev) Test() *Test {
	return &Test{Dagger: dev}
}

// Develop Dagger SDKs
func (dev *DaggerDev) SDK() *SDK {
	return &SDK{
		Go:         NewGoSDK(dev.Src, dev.Engine()),
		Python:     &PythonSDK{Dagger: dev},
		Typescript: &TypescriptSDK{Dagger: dev},
		Elixir:     &ElixirSDK{Dagger: dev},
		Rust:       &RustSDK{Dagger: dev},
		PHP:        &PHPSDK{Dagger: dev},
		Java:       &JavaSDK{Dagger: dev},
	}
}

// Develop the Dagger helm chart
func (dev *DaggerDev) Helm() *Helm {
	return &Helm{Dagger: dev, Source: dev.Source().Directory("helm/dagger")}
}

// Creates a dev container that has a running CLI connected to a dagger engine
func (dev *DaggerDev) Dev(
	ctx context.Context,
	// Mount a directory into the container's workdir, for convenience
	// +optional
	target *dagger.Directory,
	// Set target distro
	// +optional
	image *Distro,
	// Enable experimental GPU support
	// +optional
	gpuSupport bool,
) (*dagger.Container, error) {
	if target == nil {
		target = dag.Directory()
	}

	svc, err := dev.
		Engine().
		WithImage(image).
		WithGpuSupport(gpuSupport).
		Service(ctx)
	if err != nil {
		return nil, err
	}
	endpoint, err := svc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	client, err := dev.CLI().Binary(ctx, "")
	if err != nil {
		return nil, err
	}

	return dev.Go().Env().
		WithMountedDirectory("/mnt", target).
		WithMountedFile("/usr/bin/dagger", client).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/usr/bin/dagger").
		WithServiceBinding("dagger-engine", svc).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithWorkdir("/mnt"), nil
}

// Creates an static dev build
func (dev *DaggerDev) DevExport(
	ctx context.Context,
	// +optional
	platform dagger.Platform,

	// +optional
	race bool,
	// +optional
	trace bool,

	// Set target distro
	// +optional
	image *Distro,
	// Enable experimental GPU support
	// +optional
	gpuSupport bool,
) (*dagger.Directory, error) {
	var platformSpec platforms.Platform
	if platform == "" {
		platformSpec = platforms.DefaultSpec()
	} else {
		var err error
		platformSpec, err = platforms.Parse(string(platform))
		if err != nil {
			return nil, err
		}
	}

	engine := dev.Engine()
	if race {
		engine = engine.WithRace()
	}
	if trace {
		engine = engine.WithTrace()
	}
	enginePlatformSpec := platformSpec
	enginePlatformSpec.OS = "linux"
	engineCtr, err := engine.
		WithPlatform(dagger.Platform(platforms.Format(enginePlatformSpec))).
		WithImage(image).
		WithGpuSupport(gpuSupport).
		Container(ctx)
	if err != nil {
		return nil, err
	}
	engineTar := engineCtr.AsTarball(dagger.ContainerAsTarballOpts{
		// use gzip to avoid incompatibility w/ older docker versions
		ForcedCompression: dagger.Gzip,
	})

	cli := dev.CLI()
	cliBin, err := cli.Binary(ctx, platform)
	if err != nil {
		return nil, err
	}
	cliPath := "dagger"
	if platformSpec.OS == "windows" {
		cliPath += ".exe"
	}

	dir := dag.Directory().
		WithFile("engine.tar", engineTar).
		WithFile(cliPath, cliBin)
	return dir, nil
}
