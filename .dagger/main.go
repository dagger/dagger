// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct {
	Source *dagger.Directory

	Version string
	Tag     string
	Git     *dagger.GitRepository

	// Can be used by nested clients to forward docker credentials to avoid
	// rate limits
	DockerCfg *dagger.Secret // +private
}

func (dev *DaggerDev) godev() *dagger.Go {
	return dag.Go(dev.Source, dagger.GoOpts{
		// FIXME: differentiate between:
		// 1) lint exclusions,
		// 2) go mod tidy exclusions,
		// 3) dagger runtime generation exclusions
		// 4) actually building & testing stuff
		// --> maybe it's a "check exclusion"?
		Exclude: []string{
			"docs/**",
			"core/integration/**",
			"dagql/idtui/viztest/broken/**",
			"modules/evals/**",
			"**/broken*/**",
		},
		Values: []string{
			"github.com/dagger/dagger/engine.Version=" + dev.Version,
			"github.com/dagger/dagger/engine.Tag=" + dev.Tag,
		},
	})
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
	// "sdk/rust/target",
	// "sdk/php/vendor"
	// ]
	source *dagger.Directory,

	// +defaultPath="/"
	git *dagger.GitRepository,

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
		Git:       git,
		Version:   version,
		DockerCfg: dockerCfg,
	}
	return dev, nil
}

// Develop the Dagger CLI
func (dev *DaggerDev) CLI() *CLI {
	return &CLI{Dagger: dev}
}

// Bump the version of all versioned components
func (dev *DaggerDev) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
	var (
		bumpDocs, bumpHelm *dagger.Changeset
		bumpSDKs           []*dagger.Changeset
	)
	err := parallel.New().
		WithJob("bump docs version", func(ctx context.Context) error {
			var err error
			bumpDocs, err = dag.Docs().Bump(version).Sync(ctx)
			return err
		}).
		WithJob("bump helm chart version", func(ctx context.Context) error {
			chartYaml, err := dag.Helm().SetVersion(version).Sync(ctx)
			if err != nil {
				return err
			}
			bumpHelm, err = dag.Directory().
				WithFile("helm/dagger/Chart.yaml", chartYaml).
				Changes(dag.Directory()).
				Sync(ctx)
			return err
		}).
		WithJob("bump SDK versions", func(ctx context.Context) error {
			type bumper interface {
				Bump(context.Context, string) (*dagger.Changeset, error)
				Name() string
			}
			bumpers := allSDKs[bumper](dev)
			bumpSDKs = make([]*dagger.Changeset, len(bumpers))
			for i, sdk := range bumpers {
				bumped, err := sdk.Bump(ctx, version)
				if err != nil {
					return err
				}
				bumped, err = bumped.Sync(ctx)
				if err != nil {
					return err
				}
				bumpSDKs[i] = bumped
			}
			return nil
		}).
		Run(ctx)
	if err != nil {
		return nil, err
	}
	return changesetMerge(dev.Source, append(bumpSDKs, bumpDocs, bumpHelm)...), nil
}

// Run Dagger scripts
func (dev *DaggerDev) Scripts() *Scripts {
	return &Scripts{Dagger: dev}
}

// Find test suites to run
func (dev *DaggerDev) Test() *Test {
	return &Test{Dagger: dev}
}

// Find benchmark suites to run
func (dev *DaggerDev) Bench() *Bench {
	return &Bench{Test: dev.Test()}
}

// Run all code generation - SDKs, docs, grpc stubs, changelog
func (dev *DaggerDev) Generate(ctx context.Context,
	// +optional
	check bool,
) (*dagger.Changeset, error) {
	var (
		genDocs, genEngine, genChangelog, genGHA *dagger.Changeset
		genSDKs                                  []*dagger.Changeset
	)
	maybeCheck := func(ctx context.Context, cs *dagger.Changeset) (*dagger.Changeset, error) {
		if !check {
			// Always use the context, for correct span attribution
			return cs.Sync(ctx)
		}
		diffSize, err := cs.AsPatch().Size(ctx)
		if err != nil {
			return nil, err
		}
		if diffSize > 0 {
			added, err := cs.AddedPaths(ctx)
			if err != nil {
				return nil, err
			}
			removed, err := cs.RemovedPaths(ctx)
			if err != nil {
				return nil, err
			}
			modified, err := cs.ModifiedPaths(ctx)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(os.Stderr, `%d MODIFIED:
%s

%d REMOVED:
%s

%d ADDED:
%s
`,
				len(modified), strings.Join(modified, "\n"),
				len(removed), strings.Join(removed, "\n"),
				len(added), strings.Join(added, "\n"),
			)
			return cs, errors.New("generated files are not up-to-date")
		}
		return cs, nil
	}
	verb := "generate "
	if check {
		verb += "& check "
	}
	err := parallel.New().
		WithJob(verb+"docs", func(ctx context.Context) error {
			var err error
			genDocs, err = maybeCheck(ctx, dag.Docs().Generate())
			return err
		}).
		WithJob(verb+"engine", func(ctx context.Context) error {
			var err error
			genEngine, err = maybeCheck(ctx, dag.DaggerEngine().Generate())
			return err
		}).
		WithJob(verb+"changelog", func(ctx context.Context) error {
			var err error
			genChangelog, err = maybeCheck(ctx, dag.Changelog().Generate())
			return err
		}).
		WithJob(verb+"Github Actions config", func(ctx context.Context) error {
			var err error
			genGHA, err = maybeCheck(ctx, dag.Ci().Generate())
			return err
		}).
		WithJob(verb+"SDKs", func(ctx context.Context) error {
			type generator interface {
				Name() string
				Generate(context.Context) (*dagger.Changeset, error)
			}
			generators := allSDKs[generator](dev)
			genSDKs = make([]*dagger.Changeset, len(generators))
			jobs := parallel.New()
			for i, sdk := range generators {
				jobs = jobs.WithJob(sdk.Name(), func(ctx context.Context) error {
					genSDK, err := sdk.Generate(ctx)
					if err != nil {
						return err
					}
					genSDKs[i], err = maybeCheck(ctx, genSDK)
					return err
				})
			}
			return jobs.Run(ctx)
		}).
		Run(ctx)
	if err != nil {
		return nil, err
	}
	if check {
		return nil, nil
	}
	var result *dagger.Changeset
	// FIXME: this is a workaround to TUI being too noisy
	err = parallel.Run(ctx, "merge all changesets", func(ctx context.Context) error {
		var err error
		changes := slices.Clone(genSDKs)
		changes = append(changes, genDocs, genEngine, genChangelog, genGHA)
		result, err = changesetMerge(dev.Source, changes...).Sync(ctx)
		return err
	})
	return result, err
}

// Develop Dagger SDKs
func (dev *DaggerDev) SDK() *SDK {
	return &SDK{
		Dagger:     dev, // for generating changesets on generate. Remove once Changesets can be merged
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
	// +defaultPath="/"
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
	return dev.godev().Env().
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
