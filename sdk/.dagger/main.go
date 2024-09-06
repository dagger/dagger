package main

import (
	"context"
	"dagger/sdk/internal/dagger"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/dagger/dagger/.dagger/consts"
	"github.com/moby/buildkit/identity"
)

type Sdk struct{}

func (sdk *Sdk) ChangeNotes(source *dagger.Directory, version string) *dagger.File {
	return source.File(fmt.Sprintf(".changes/%s.md", version))
}

// Publish an SDK to a git repository
func (sdk *Sdk) GitPublish(
	ctx context.Context,
	// Source repository URL
	// +optional
	source string,
	// Destination repository URL
	// +optional
	dest string,
	// Tag or reference in the source repository
	// +optional
	sourceTag string,
	// Tag or reference in the destination repository
	// +optional
	destTag string,
	// Path within the source repository to publish
	// +optional
	sourcePath string,
	// Filter to apply to the source files
	// +optional
	sourceFilter string,
	// Container environment for source operations
	// +optional
	sourceEnv *dagger.Container,
	// Git username for commits
	// +optional
	username string,
	// Git email for commits
	// +optional
	email string,
	// GitHub token for authentication
	// +optional
	githubToken *dagger.Secret,
	// Whether to perform a dry run without pushing changes
	// +optional
	dryRun bool,
) error {
	base := sourceEnv
	if base == nil {
		base = dag.Container().
			From(consts.AlpineImage).
			WithExec([]string{"apk", "add", "-U", "--no-cache", "git", "go", "python3"})
	}

	// FIXME: move this into std modules
	git := base.
		WithExec([]string{"git", "config", "--global", "user.name", username}).
		WithExec([]string{"git", "config", "--global", "user.email", email})
	if !dryRun {
		githubTokenRaw, err := githubToken.Plaintext(ctx)
		if err != nil {
			return err
		}
		encodedPAT := base64.URLEncoding.EncodeToString([]byte("pat:" + githubTokenRaw))
		git = git.
			WithEnvVariable("GIT_CONFIG_COUNT", "1").
			WithEnvVariable("GIT_CONFIG_KEY_0", "http.https://github.com/.extraheader").
			WithSecretVariable("GIT_CONFIG_VALUE_0", dag.SetSecret("GITHUB_HEADER", fmt.Sprintf("AUTHORIZATION: Basic %s", encodedPAT)))
	}

	result := git.
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithWorkdir("/src/dagger").
		WithExec([]string{"git", "clone", source, "."}).
		WithExec([]string{"git", "fetch", "origin", "-v", "--update-head-ok", fmt.Sprintf("refs/*%[1]s:refs/*%[1]s", strings.TrimPrefix(sourceTag, "refs/"))}).
		WithEnvVariable("FILTER_BRANCH_SQUELCH_WARNING", "1").
		WithExec([]string{
			"git", "filter-branch", "-f", "--prune-empty",
			"--subdirectory-filter", sourcePath,
			"--tree-filter", sourceFilter,
			"--", sourceTag,
		})
	if !dryRun {
		result = result.WithExec([]string{
			"git",
			"push",
			// "--force", // NOTE: disabled to avoid accidentally rewriting the history
			dest,
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

		destCommit, err := git.
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithWorkdir("/src/dagger").
			WithExec([]string{"git", "clone", dest, "."}).
			WithExec([]string{"git", "fetch", "origin", "-v", "--update-head-ok", fmt.Sprintf("refs/*%[1]s:refs/*%[1]s", strings.TrimPrefix(destTag, "refs/"))}).
			WithExec([]string{"git", "checkout", destTag, "--"}).
			WithExec([]string{"git", "rev-parse", "HEAD"}).
			Stdout(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "invalid reference: "+destTag) {
				// this is a ref that only exists in the source, and not in the
				// dest, so no overwriting will occur
				return nil
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

// Publish a Github release
func (sdk *Sdk) GithubRelease(
	ctx context.Context,
	// Tag for the GitHub release
	// +optional
	tag string,
	// File containing release notes
	// +optional
	notes *dagger.File,
	// GitHub repository URL
	// +optional
	gitRepo string,
	// GitHub token for authentication
	// +optional
	githubToken *dagger.Secret,
	// Whether to perform a dry run without creating the release
	// +optional
	dryRun bool,
) error {
	u, err := url.Parse(gitRepo)
	if err != nil {
		return err
	}
	if u.Host != "github.com" {
		return fmt.Errorf("git repo must be on github.com")
	}
	githubRepo := strings.TrimPrefix(strings.TrimSuffix(u.Path, ".git"), "/")

	if dryRun {
		// sanity check tag is in target repo
		_, err = dag.
			Git(fmt.Sprintf("https://github.com/%s", githubRepo)).
			Ref(tag).
			Tree().
			Sync(ctx)
		if err != nil {
			return err
		}

		// sanity check notes file exists
		notesContent, err := notes.Contents(ctx)
		if err != nil {
			return err
		}
		fmt.Println(notesContent)

		return nil
	}

	gh := dag.Gh(dagger.GhOpts{
		Repo:  githubRepo,
		Token: githubToken,
	})
	return gh.Release().Create(
		ctx,
		tag,
		tag,
		dagger.GhReleaseCreateOpts{
			VerifyTag: true,
			Draft:     true,
			NotesFile: notes,
			// Latest:    false,  // can't do this yet
		},
	)
}
