package main

import (
	"context"
	"crypto/rand"
	"dagger/git-releaser/internal/dagger"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

type GitReleaser struct {
	AlpineVersion string // +private
}

func New(
	// +default="3.22.1"
	// +optional
	alpineVersion string,
) *GitReleaser {
	return &GitReleaser{
		AlpineVersion: alpineVersion,
	}
}

// Execute a dry-run release,
// to verify that a release is possible without actually completing it
func (gitrel *GitReleaser) DryRun(
	ctx context.Context,
	sourceRepo *dagger.GitRepository,
	sourceTag string,
	destRemote string,

	destTag string, // +optional
	sourcePath string, // +optional
	callback *dagger.File, // +optional
) error {
	return gitrel.Release(ctx,
		sourceRepo,
		sourceTag,
		destRemote,

		destTag,
		sourcePath,
		callback,

		nil,  // githubToken
		true, // dryRun
	)
}

// Execute a source release from a git repository
func (gitrel *GitReleaser) Release(
	ctx context.Context,
	// The git repository to release from
	sourceRepo *dagger.GitRepository,
	// Local tag to release from
	sourceTag string,
	// Git remote to push the release to
	destRemote string,
	// Remote tag to release to
	destTag string, // +optional
	// Optionally publish only a subdirectory
	sourcePath string, // +optional
	// Python script executed by git-filter-repo
	// See https://github.com/newren/git-filter-repo/blob/main/Documentation/converting-from-filter-branch.md#cheat-sheet-additional-conversion-examples
	callback *dagger.File, // +optional
	// Github authentication token
	githubToken *dagger.Secret, // +optional
	// Execute a dry run without actually releasing
	dryRun bool, // +optional
) error {
	if destTag == "" {
		destTag = sourceTag
	}
	base := dag.
		Alpine(dagger.AlpineOpts{
			Branch:   gitrel.AlpineVersion,
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

	if !dryRun && githubToken != nil {
		githubTokenRaw, err := githubToken.Plaintext(ctx)
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
	if sourcePath != "" {
		// only extract the source path
		filterRepoArgs = append(filterRepoArgs, "--subdirectory-filter", sourcePath)
	}
	if callback != nil {
		callbackText, err := callback.Contents(ctx)
		if err != nil {
			return err
		}
		filterRepoArgs = append(filterRepoArgs, "--file-info-callback", callbackText)
	}

	result := base.
		WithEnvVariable("CACHEBUSTER", rand.Text()).
		WithWorkdir("/src/dagger").
		WithDirectory(".", sourceRepo.Ref(sourceTag).Tree(dagger.GitRefTreeOpts{Depth: -1})).
		WithExec(filterRepoArgs)
	if !dryRun {
		result = result.WithExec([]string{
			"git",
			"push",
			// "--force", // NOTE: disabled to avoid accidentally rewriting the history
			destRemote,
			fmt.Sprintf("%s:%s", sourceTag, destTag),
		})
	} else {
		// on a dry run, just test that the last state of dest is in the current branch (and is a fast-forward)
		history, err := result.
			WithExec([]string{"git", "log", "--oneline", "--no-abbrev-commit", sourceTag}).
			Stdout(ctx)
		if err != nil {
			return err
		}

		destCommit, err := base.
			WithEnvVariable("CACHEBUSTER", rand.Text()).
			WithWorkdir("/src/dagger").
			WithExec([]string{"git", "clone", destRemote, "."}).
			WithExec([]string{"git", "fetch", "origin", "-v", "--update-head-ok", fmt.Sprintf("refs/*%[1]s:refs/*%[1]s", strings.TrimPrefix(destTag, "refs/"))}).
			WithExec([]string{"git", "checkout", destTag, "--"}).
			WithExec([]string{"git", "rev-parse", "HEAD"}).
			Stdout(ctx)
		if err != nil {
			var execErr *dagger.ExecError
			if errors.As(err, &execErr) {
				if strings.Contains(execErr.Stderr, "invalid reference: "+destTag) {
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
