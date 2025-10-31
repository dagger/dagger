package main

import (
	"context"
	"dagger/releaser/internal/dagger"
	"fmt"
	"net/url"
	"strings"
)

// Publish a Github release
func (r Releaser) githubRelease(
	ctx context.Context,
	// GitHub repository URL
	repository string,
	// Source commit for the GitHub release
	// eg. ec9686a4b922e278614ed1754d308c75eaa59586 v0.14.0
	src string,
	// The target tag for the component release
	// e.g. sdk/typescript/v0.14.0
	dest string,
	// File containing release notes
	// +optional
	notes *dagger.File,
	// GitHub token for authentication
	// +optional
	token *dagger.Secret,
	// Whether to perform a dry run without creating the release
	// +optional
	dryRun bool,
) error {
	githubRepo, err := githubRepo(repository)
	if err != nil {
		return err
	}

	if dryRun {
		// Check that the src commit is in the repo
		_, err = dag.
			Git(fmt.Sprintf("https://github.com/%s", githubRepo)).
			Commit(src).
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
		Token: token,
	})
	return gh.Release().Create(
		ctx,
		dest,
		dest,
		dagger.GhReleaseCreateOpts{
			Target:    src,
			NotesFile: notes,
			Latest:    dagger.GhLatestLatestFalse,
		},
	)
}

func githubRepo(repo string) (string, error) {
	u, err := url.Parse(repo)
	if err != nil {
		return "", err
	}
	if (u.Host != "") && (u.Host != "github.com") {
		return "", fmt.Errorf("git repo must be on github.com")
	}
	return strings.TrimPrefix(strings.TrimSuffix(u.Path, ".git"), "/"), nil
}
