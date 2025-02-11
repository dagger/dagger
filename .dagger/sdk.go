package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/moby/buildkit/identity"

	"github.com/dagger/dagger/.dagger/build"
	"github.com/dagger/dagger/.dagger/consts"
	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// A dev environment for the official Dagger SDKs
type SDK struct {
	// Develop the Dagger Go SDK
	Go *GoSDK
	// Develop the Dagger Python SDK
	Python *PythonSDK
	// Develop the Dagger Typescript SDK
	Typescript *TypescriptSDK

	// Develop the Dagger Elixir SDK (experimental)
	Elixir *ElixirSDK
	// Develop the Dagger Rust SDK (experimental)
	Rust *RustSDK
	// Develop the Dagger PHP SDK (experimental)
	PHP *PHPSDK
	// Develop the Dagger Java SDK (experimental)
	Java *JavaSDK
	// Develop the Dagger Dotnet SDK (experimental)
	Dotnet *DotnetSDK
}

func (sdk *SDK) All() *AllSDK {
	return &AllSDK{
		SDK: sdk,
	}
}

type sdkBase interface {
	Lint(ctx context.Context) error
	Test(ctx context.Context) error
	TestPublish(ctx context.Context, tag string) error
	Generate(ctx context.Context) (*dagger.Directory, error)
	Bump(ctx context.Context, version string) (*dagger.Directory, error)
}

func (sdk *SDK) allSDKs() []sdkBase {
	return []sdkBase{
		sdk.Go,
		sdk.Python,
		sdk.Typescript,
		sdk.Elixir,
		sdk.Rust,
		sdk.PHP,
		sdk.Java,
		sdk.Dotnet,
	}
}

func (dev *DaggerDev) installer(ctx context.Context, name string) (func(*dagger.Container) *dagger.Container, error) {
	engineSvc, err := dev.Engine().Service(ctx, name, nil, false, false)
	if err != nil {
		return nil, err
	}

	cliBinary := dag.DaggerCli().Binary()
	cliBinaryPath := "/.dagger-cli"

	return func(ctr *dagger.Container) *dagger.Container {
		ctr = ctr.
			WithServiceBinding("dagger-engine", engineSvc).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dagger-engine:1234").
			WithMountedFile(cliBinaryPath, cliBinary).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinaryPath).
			WithExec([]string{"ln", "-s", cliBinaryPath, "/usr/local/bin/dagger"}).
			With(dev.withDockerCfg) // this avoids rate limiting in our ci tests
		return ctr
	}, nil
}

func (dev *DaggerDev) introspection(ctx context.Context, installer func(*dagger.Container) *dagger.Container) (*dagger.File, error) {
	builder, err := build.NewBuilder(ctx, dev.Source())
	if err != nil {
		return nil, err
	}
	return dag.
		Alpine(dagger.AlpineOpts{
			Branch: consts.AlpineVersion,
		}).
		Container().
		With(installer).
		WithFile("/usr/local/bin/codegen", builder.CodegenBinary()).
		WithExec([]string{"codegen", "introspect", "-o", "/schema.json"}).
		File("/schema.json"), nil
}

type gitPublishOpts struct {
	sdk string

	source, dest       string
	sourceTag, destTag string
	sourcePath         string
	sourceFilter       string
	sourceEnv          *dagger.Container

	username    string
	email       string
	githubToken *dagger.Secret

	dryRun bool
}

func gitPublish(ctx context.Context, git *dagger.VersionGit, opts gitPublishOpts) error {
	base := opts.sourceEnv
	if base == nil {
		base = dag.
			Alpine(dagger.AlpineOpts{
				Branch:   consts.AlpineVersion,
				Packages: []string{"git", "go", "python3"},
			}).
			Container()
	}

	base = base.
		WithExec([]string{"git", "config", "--global", "user.name", opts.username}).
		WithExec([]string{"git", "config", "--global", "user.email", opts.email})
	if !opts.dryRun {
		githubTokenRaw, err := opts.githubToken.Plaintext(ctx)
		if err != nil {
			return err
		}
		encodedPAT := base64.URLEncoding.EncodeToString([]byte("pat:" + githubTokenRaw))
		base = base.
			WithEnvVariable("GIT_CONFIG_COUNT", "1").
			WithEnvVariable("GIT_CONFIG_KEY_0", "http.https://github.com/.extraheader").
			WithSecretVariable("GIT_CONFIG_VALUE_0", dag.SetSecret("GITHUB_HEADER", fmt.Sprintf("AUTHORIZATION: Basic %s", encodedPAT)))
	}

	result := base.
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithWorkdir("/src/dagger").
		WithDirectory(".", git.Directory()).
		WithExec([]string{"git", "restore", "."}). // clean up the dirty state
		WithEnvVariable("FILTER_BRANCH_SQUELCH_WARNING", "1").
		WithExec([]string{
			"git", "filter-branch", "-f", "--prune-empty",
			"--subdirectory-filter", opts.sourcePath,
			"--tree-filter", opts.sourceFilter,
			"--", opts.sourceTag,
		})
	if !opts.dryRun {
		result = result.WithExec([]string{
			"git",
			"push",
			// "--force", // NOTE: disabled to avoid accidentally rewriting the history
			opts.dest,
			fmt.Sprintf("%s:%s", opts.sourceTag, opts.destTag),
		})
	} else {
		// on a dry run, just test that the last state of dest is in the current branch (and is a fast-forward)
		history, err := result.
			WithExec([]string{"git", "log", "--oneline", "--no-abbrev-commit", opts.sourceTag}).
			Stdout(ctx)
		if err != nil {
			return err
		}

		destCommit, err := base.
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithWorkdir("/src/dagger").
			WithExec([]string{"git", "clone", opts.dest, "."}).
			WithExec([]string{"git", "fetch", "origin", "-v", "--update-head-ok", fmt.Sprintf("refs/*%[1]s:refs/*%[1]s", strings.TrimPrefix(opts.destTag, "refs/"))}).
			WithExec([]string{"git", "checkout", opts.destTag, "--"}).
			WithExec([]string{"git", "rev-parse", "HEAD"}).
			Stdout(ctx)
		if err != nil {
			var execErr *dagger.ExecError
			if errors.As(err, &execErr) {
				if strings.Contains(execErr.Stderr, "invalid reference: "+opts.destTag) {
					// this is a ref that only exists in the source, and not in the
					// dest, so no overwriting will occur
					return nil
				}
			}
			return err
		}
		destCommit = strings.TrimSpace(destCommit)

		if !strings.Contains(history, destCommit) {
			return fmt.Errorf("publish would rewrite history - %s not found\n%s", destCommit, history)
		}
		return nil
	}

	_, err := result.Sync(ctx)
	return err
}
