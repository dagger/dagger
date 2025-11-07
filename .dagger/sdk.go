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

// Develop Dagger SDKs
func (dev *DaggerDev) SDK() *SDK {
	return &SDK{
		Dagger:     dev, // for generating changesets on generate. Remove once Changesets can be merged
		Python:     &PythonSDK{Dagger: dev},
		Typescript: &TypescriptSDK{Dagger: dev},
		Elixir:     &ElixirSDK{Dagger: dev},
		Rust:       &RustSDK{Dagger: dev},
		Java:       &JavaSDK{Dagger: dev},
	}
}

// A dev environment for the official Dagger SDKs
type SDK struct {
	Dagger *DaggerDev // +private
	// Develop the Dagger Python SDK
	Python *PythonSDK
	// Develop the Dagger Typescript SDK
	Typescript *TypescriptSDK
	// Develop the Dagger Elixir SDK (experimental)
	Elixir *ElixirSDK
	// Develop the Dagger Rust SDK (experimental)
	Rust *RustSDK
	// Develop the Dagger Java SDK (experimental)
	Java *JavaSDK
}

// Return an "installer" function which, given a container, will attach
// a dev engine and CLI
func (dev *DaggerDev) devEngineSidecar() func(*dagger.Container) *dagger.Container {
	return func(client *dagger.Container) *dagger.Container {
		return dag.DaggerEngine().InstallClient(client)
	}
}

type namedSDK[T any] struct {
	Name  string
	Value T
}

// Return a list of all SDKs implementing the given interface
func allSDKs[T any](dev *DaggerDev) []namedSDK[T] {
	var result []namedSDK[T]
	sdks := dev.SDK()
	for _, entry := range []struct {
		name string
		sdk  any
	}{
		{"go", dag.GoSDK()},
		{"python", sdks.Python},
		{"typescript", sdks.Typescript},
		{"elixir", sdks.Elixir},
		{"rust", sdks.Rust},
		{"php", dag.PhpSDK()},
		{"java", sdks.Java},
		{"dotnet", sdks.selfCallDotnet()},
	} {
		if casted, ok := entry.sdk.(T); ok {
			result = append(result, namedSDK[T]{
				Name:  entry.name,
				Value: casted,
			})
		}
	}
	return result
}

// Return the introspection.json from the current dev engine
func (dev *DaggerDev) introspectionJSON() *dagger.File {
	return dag.DaggerEngine().IntrospectionJSON()
}

type gitPublishOpts struct {
	sdk string

	dest               string
	sourceTag, destTag string
	sourcePath         string

	callback string

	githubToken *dagger.Secret

	dryRun bool
}

func gitPublish(ctx context.Context, git *dagger.GitRepository, opts gitPublishOpts) error {
	base := dag.
		Alpine(dagger.AlpineOpts{
			Branch:   distconsts.AlpineVersion,
			Packages: []string{"git", "go", "python3"},
		}).
		Container()

	// git-filter-repo is a better alternative to git-filter-branch
	gitFilterRepoVersion := "v2.47.0"
	base = base.WithFile(
		"/usr/local/bin/git-filter-repo",
		dag.HTTP(fmt.Sprintf("https://raw.githubusercontent.com/newren/git-filter-repo/%s/git-filter-repo", gitFilterRepoVersion)),
		dagger.ContainerWithFileOpts{Permissions: 0755},
	)

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

	filterRepoArgs := []string{
		"git", "filter-repo",
		// this repo doesn't *look* like a fresh clone, so disable the safety check
		"--force",
	}

	// NOTE: these are required for compatibility with git-filter-branch
	// without these, we would end up rewriting the dagger-go-sdk history,
	// which would be very sad for out integration with the go ecosystem
	filterRepoArgs = append(filterRepoArgs,
		// prune all commits that have no effect on the history
		"--prune-empty=always", "--prune-degenerate=always",
		// keep commit hashes in commit messages as-is :(
		"--preserve-commit-hashes",
	)
	if opts.sourcePath != "" {
		filterRepoArgs = append(filterRepoArgs,
			// only extract the source path
			"--subdirectory-filter", opts.sourcePath,
		)
	}
	if opts.callback != "" {
		filterRepoArgs = append(filterRepoArgs,
			// apply a callback
			"--file-info-callback", opts.callback,
		)
	}

	result := base.
		WithEnvVariable("CACHEBUSTER", rand.Text()).
		WithWorkdir("/src/dagger").
		WithDirectory(".", git.Ref(opts.sourceTag).Tree(dagger.GitRefTreeOpts{Depth: -1})).
		WithExec(filterRepoArgs)
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
