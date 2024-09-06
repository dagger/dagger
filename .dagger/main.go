// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"
	"fmt"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/.dagger/internal/dagger"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct {
	// Can be used by nested clients to forward docker credentials to avoid
	// rate limits
	DockerCfg *dagger.Secret // +private
}

func New(
	ctx context.Context,
	// +optional
	dockerCfg *dagger.Secret,
) (*DaggerDev, error) {
	return &DaggerDev{
		DockerCfg: dockerCfg,
	}, nil
}

func (dev *DaggerDev) Generate(
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!**/dagger.json", "!**/.dagger", "!modules/**"]
	daggerModules *dagger.Directory,
) *dagger.Directory {
	return dirMerge([]*dagger.Directory{
		dev.GenerateDaggerModules(daggerModules),
		// Re-generate docs
		dag.Docs().Generate(),
		// Re-generate Go SDK client library
		dag.GoSDK().Generate(),
	})
}

// Re-generate all dagger modules
func (dev *DaggerDev) GenerateDaggerModules(
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!**/dagger.json", "!**/.dagger", "!modules/**"]
	source *dagger.Directory,
) *dagger.Directory {
	return dag.Supermod(source).
		DevelopAll(dagger.SupermodDevelopAllOpts{Exclude: []string{
			"docs/.*",
			"core/integration/.*",
		}}).Source()
}

func dirMerge(dirs []*dagger.Directory) *dagger.Directory {
	var out *dagger.Directory
	for _, dir := range dirs {
		if out == nil {
			out = dir
		} else {
			out = out.WithDirectory("", dir)
		}
	}
	return out
}

// Lint the Dagger source code
func (dev *DaggerDev) Lint(
	ctx context.Context,
) error {
	eg, ctx := errgroup.WithContext(ctx)
	// Go SDK lint
	eg.Go(func() error {
		return dag.GoSDK().Lint(ctx)
	})
	// Fixme: lint other SDKs
	return eg.Wait()
}

type Check func(context.Context) error

// Wrap 3 SDK-specific checks into a single check
type SDKChecks interface {
	Lint(ctx context.Context) error
	Test(ctx context.Context) error
	TestPublish(ctx context.Context, tag string) error
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
	gpu bool,
) (*dagger.Container, error) {
	if target == nil {
		target = dag.Directory()
	}
	return dag.
		Engine(dagger.EngineOpts{
			Image: image,
			Gpu:   gpu,
		}).
		Container().
		WithMountedDirectory("/root", target).
		WithWorkdir("/root")
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
