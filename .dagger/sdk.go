package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/engine/distconsts"
)

// A dev environment for the official Dagger SDKs
type SDK struct {
	Dagger *DaggerDev // +private
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

// Return an "installer" function which, given a container, will attach
// a dev engine and CLI
func (dev *DaggerDev) devEngineSidecar() func(*dagger.Container) *dagger.Container {
	// The name "sdk" is an arbitrary key for engine state reuse across builds
	instanceName := "sdk"
	engineSvc := dag.DaggerEngine().Service(instanceName)
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
	}
}

// Return a list of all SDKs implementing the given interface
func allSDKs[T any](dev *DaggerDev) []T {
	var result []T
	for _, sdk := range []any{
		&GoSDK{Dagger: dev},
		&PythonSDK{Dagger: dev},
		&TypescriptSDK{Dagger: dev},
		&ElixirSDK{Dagger: dev},
		&RustSDK{Dagger: dev},
		&PHPSDK{Dagger: dev},
		&JavaSDK{Dagger: dev},
		&DotnetSDK{Dagger: dev},
	} {
		if casted, ok := sdk.(T); ok {
			result = append(result, casted)
		}
	}
	return result
}

func (dev *DaggerDev) codegenBinary() *dagger.File {
	return dev.godev().Binary("./cmd/codegen", dagger.GoBinaryOpts{
		NoSymbols: true,
		NoDwarf:   true,
	})
}

// Return the introspection.json from the current dev engine
func (dev *DaggerDev) introspectionJSON() *dagger.File {
	return dag.
		Alpine(dagger.AlpineOpts{
			Branch: distconsts.AlpineVersion,
		}).
		Container().
		With(dev.devEngineSidecar()).
		WithFile("/usr/local/bin/codegen", dev.codegenBinary()).
		WithExec([]string{"codegen", "introspect", "-o", "/schema.json"}).
		File("/schema.json")
}

type gitPublishOpts struct {
	sdk string

	dest               string
	sourceTag, destTag string
	sourcePath         string
	sourceFilter       string
	sourceEnv          *dagger.Container

	username    string
	email       string
	githubToken *dagger.Secret

	dryRun bool
}

func gitPublish(ctx context.Context, git *dagger.GitRepository, opts gitPublishOpts) error {
	base := opts.sourceEnv
	if base == nil {
		base = dag.
			Alpine(dagger.AlpineOpts{
				Branch:   distconsts.AlpineVersion,
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
		WithEnvVariable("CACHEBUSTER", rand.Text()).
		WithWorkdir("/src/dagger").
		WithDirectory(".", git.Ref(opts.sourceTag).Tree(dagger.GitRefTreeOpts{Depth: -1})).
		WithEnvVariable("FILTER_BRANCH_SQUELCH_WARNING", "1").
		WithExec([]string{"git", "status"}).
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
			WithEnvVariable("CACHEBUSTER", rand.Text()).
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
			return fmt.Errorf("publish would rewrite history - %s not found", destCommit)
		}
		return nil
	}

	_, err := result.Sync(ctx)
	return err
}
