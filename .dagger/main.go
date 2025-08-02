// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"golang.org/x/sync/errgroup"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct {
	Source *dagger.Directory

	Version string
	Tag     string
	Git     *dagger.VersionGit // +private

	// When set, module codegen is automatically applied when retrieving the Dagger source code
	ModCodegenTargets []string

	// Can be used by nested clients to forward docker credentials to avoid
	// rate limits
	DockerCfg *dagger.Secret // +private
}

func New(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	// +ignore=[
	// "bin",
	// ".git",
	// "**/node_modules",
	// "**/.venv",
	// "**/__pycache__",
	// "docs/node_modules",
	// "sdk/typescript/node_modules",
	// "sdk/typescript/dist",
	// "sdk/rust/examples/backend/target",
	// "sdk/rust/target"
	// ]
	source *dagger.Directory,

	// +optional
	dockerCfg *dagger.Secret,
) (*DaggerDev, error) {
	v := dag.Version()
	version, err := v.Version(ctx)
	if err != nil {
		return nil, err
	}
	tag, err := v.ImageTag(ctx)
	if err != nil {
		return nil, err
	}

	dev := &DaggerDev{
		Source:    source,
		Tag:       tag,
		Git:       v.Git(),
		Version:   version,
		DockerCfg: dockerCfg,
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
		if strings.HasPrefix(module, "dagql/idtui/viztest/broken") {
			continue
		}
		if strings.HasPrefix(module, "modules/evaluator/") {
			continue
		}
		dev.ModCodegenTargets = append(dev.ModCodegenTargets, module)
	}

	return dev, nil
}

func (dev *DaggerDev) SourceDeveloped(targets ...string) *dagger.Directory {
	if targets == nil {
		targets = dev.ModCodegenTargets
	}
	src := dev.Source
	for _, module := range targets {
		layer := dev.Source.
			AsModule(dagger.DirectoryAsModuleOpts{
				SourceRootPath: module,
			}).
			GeneratedContextDirectory().
			Directory(module)
		src = src.WithDirectory(module, layer)
	}
	return src
}

// Develop the Dagger CLI
func (dev *DaggerDev) CLI() *CLI {
	return &CLI{Dagger: dev}
}

// Lint the codebase
func (dev *DaggerDev) Lint(
	ctx context.Context,
	pkgs []string, // +optional
) error {
	eg := errgroup.Group{}
	eg.Go(func() error {
		if len(pkgs) == 0 {
			allPkgs, err := dev.containing(ctx, "go.mod")
			if err != nil {
				return err
			}

			for _, pkg := range allPkgs {
				if strings.HasPrefix(pkg, "docs/") {
					continue
				}
				if strings.HasPrefix(pkg, "core/integration/") {
					continue
				}
				if strings.HasPrefix(pkg, "dagql/idtui/viztest/broken") {
					continue
				}
				if strings.HasPrefix(pkg, "modules/evaluator/") {
					continue
				}
				pkgs = append(pkgs, pkg)
			}
		}

		return dag.
			Go(dev.SourceDeveloped()).
			Lint(ctx, dagger.GoLintOpts{Packages: pkgs})
	})
	eg.Go(func() error {
		return dag.DaggerEngine().LintGenerate(ctx)
	})

	return eg.Wait()
}

func (dev *DaggerDev) containing(ctx context.Context, filename string) ([]string, error) {
	entries, err := dev.Source.Glob(ctx, "**/"+filename)
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
	return &GoToolchain{Go: dag.Go(dev.Source, dagger.GoOpts{
		Values: []string{
			"github.com/dagger/dagger/engine.Version=" + dev.Version,
			"github.com/dagger/dagger/engine.Tag=" + dev.Tag,
		},
	})}
}

type GoToolchain struct {
	// NOTE: this wrapper is because we can't return Go directly :(
	// +private
	*dagger.Go
}

func (gtc *GoToolchain) Env() *dagger.Container {
	return gtc.Go.Env()
}

// Run Dagger scripts
func (dev *DaggerDev) Scripts() *Scripts {
	return &Scripts{Dagger: dev}
}

// Find test suites to run
func (dev *DaggerDev) Test() *Test {
	return &Test{Dagger: dev}
}

// Run the Dagger evals across the major model providers.
func (dev *DaggerDev) Evals(
	ctx context.Context,
	// Run particular evals, or all evals if unspecified.
	// +optional
	evals []string,
	// Run particular models, or all models if unspecified.
	// +optional
	models []string,
) error {
	return dev.evaluator().
		EvalsAcrossModels(dagger.EvaluatorEvalsAcrossModelsOpts{
			Evals:  evals,
			Models: models,
		}).
		Check(ctx)
}

func (dev *DaggerDev) evaluator() *dagger.Evaluator {
	return dag.Evaluator().
		WithDocsFile(dev.Source.File("core/llm_docs.md")).
		WithoutDefaultSystemPrompt().
		WithSystemPromptFile(dev.Source.File("core/llm_dagger_prompt.md")).
		WithEvals([]*dagger.EvaluatorEval{
			// FIXME: ideally this list would live closer to where the evals are
			// defined, but it's not possible for a module to return an interface type
			// https://github.com/dagger/dagger/issues/7582
			dag.Evals().Basic().AsEvaluatorEval(),
			dag.Evals().BuildMulti().AsEvaluatorEval(),
			dag.Evals().BuildMultiNoVar().AsEvaluatorEval(),
			dag.Evals().WorkspacePattern().AsEvaluatorEval(),
			dag.Evals().ReadImplicitVars().AsEvaluatorEval(),
			dag.Evals().UndoChanges().AsEvaluatorEval(),
			dag.Evals().CoreAPI().AsEvaluatorEval(),
			dag.Evals().ModuleDependencies().AsEvaluatorEval(),
			dag.Evals().Responses().AsEvaluatorEval(),
		})
}

// Find benchmark suites to run
func (dev *DaggerDev) Bench() *Bench {
	return &Bench{Test: dev.Test()}
}

// Run all code generation - SDKs, docs, etc
func (dev *DaggerDev) Generate(ctx context.Context) (*dagger.Directory, error) {
	var docs, sdks, engine *dagger.Directory
	var eg errgroup.Group

	eg.Go(func() error {
		var err error
		docs = dag.Docs().Generate()
		docs, err = docs.Sync(ctx)
		return err
	})

	eg.Go(func() error {
		var err error
		sdks, err = dev.SDK().All().Generate(ctx)
		if err != nil {
			return err
		}
		sdks, err = sdks.Sync(ctx)
		return err
	})

	eg.Go(func() error {
		var err error
		engine = dag.DaggerEngine().Generate()
		engine, err = engine.Sync(ctx)
		return err
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return dag.Directory().
		WithDirectory("", docs).
		WithDirectory("", sdks).
		WithDirectory("", engine), nil
}

// Develop Dagger SDKs
func (dev *DaggerDev) SDK() *SDK {
	return &SDK{
		Go:         &GoSDK{Dagger: dev},
		Python:     &PythonSDK{Dagger: dev},
		Typescript: &TypescriptSDK{Dagger: dev},
		Elixir:     &ElixirSDK{Dagger: dev},
		Rust:       &RustSDK{Dagger: dev},
		PHP:        &PHPSDK{Dagger: dev},
		Java:       &JavaSDK{Dagger: dev},
		Dotnet:     &DotnetSDK{Dagger: dev},
	}
}

// Creates a dev container that has a running CLI connected to a dagger engine
func (dev *DaggerDev) Dev(
	ctx context.Context,
	// Mount a directory into the container's workdir, for convenience
	// +optional
	target *dagger.Directory,
	// Set target distro
	// +default="alpine"
	image Distro,
	// Enable experimental GPU support
	// +optional
	gpuSupport bool,
	// Share cache globally
	// +optional
	sharedCache bool,
) (*dagger.Container, error) {
	if target == nil {
		target = dag.Directory()
	}

	svc := dag.DaggerEngine().Service("", dagger.DaggerEngineServiceOpts{
		Image:       dagger.DaggerEngineDistro(image),
		GpuSupport:  gpuSupport,
		SharedCache: sharedCache,
	})
	endpoint, err := svc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}
	return dev.Go().Env().
		WithMountedDirectory("/mnt", target).
		WithMountedFile("/usr/bin/dagger", dag.DaggerCli().Binary()).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/usr/bin/dagger").
		WithServiceBinding("dagger-engine", svc).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithWorkdir("/mnt"), nil
}

func (dev *DaggerDev) withDockerCfg(ctr *dagger.Container) *dagger.Container {
	if dev.DockerCfg == nil {
		return ctr
	}
	return ctr.WithMountedSecret("/root/.docker/config.json", dev.DockerCfg)
}

func (dev *DaggerDev) Scan(ctx context.Context) error {
	ignoreFiles := dag.Directory().WithDirectory("/", dev.Source, dagger.DirectoryWithDirectoryOpts{
		Include: []string{
			".trivyignore",
			".trivyignore.yml",
			".trivyignore.yaml",
		},
	})
	ignoreFileNames, err := ignoreFiles.Entries(ctx)
	if err != nil {
		return err
	}

	ctr := dag.Container().
		From("aquasec/trivy:0.64.1@sha256:a8ca29078522f30393bdb34225e4c0994d38f37083be81a42da3a2a7e1488e9e").
		WithMountedDirectory("/mnt/ignores", ignoreFiles).
		WithMountedCache("/root/.cache/", dag.CacheVolume("trivy-cache")).
		With(dev.withDockerCfg)

	commonArgs := []string{
		"--format=json",
		"--exit-code=1",
		"--severity=CRITICAL,HIGH",
		"--show-suppressed",
	}
	if len(ignoreFileNames) > 0 {
		commonArgs = append(commonArgs, "--ignorefile=/mnt/ignores/"+ignoreFileNames[0])
	}

	eg := errgroup.Group{}

	eg.Go(func() error {
		// scan the source code
		args := []string{
			"trivy",
			"fs",
			"--scanners=vuln",
			"--pkg-types=library",
		}
		args = append(args, commonArgs...)
		args = append(args, "/mnt/src")

		// HACK: filter out directories that present occasional issues
		src := dev.Source
		src = src.
			WithoutDirectory("docs").
			WithoutDirectory("sdk/rust/crates/dagger-sdk/examples").
			WithoutDirectory("core/integration/testdata").
			WithoutDirectory("dagql/idtui/viztest")

		_, err := ctr.
			WithMountedDirectory("/mnt/src", src).
			WithExec(args).
			Sync(ctx)
		return err
	})

	eg.Go(func() error {
		// scan the engine image - this can catch dependencies that are only
		// discoverable in the final build
		args := []string{
			"trivy",
			"image",
			"--pkg-types=os,library",
		}
		args = append(args, commonArgs...)
		engineTarball := "/mnt/engine.tar"
		args = append(args, "--input", engineTarball)

		target := dag.DaggerEngine().Container()
		_, err = ctr.
			WithMountedFile(engineTarball, target.AsTarball()).
			WithExec(args).
			Sync(ctx)
		return err
	})

	return eg.Wait()
}
