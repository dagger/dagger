package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"toolchains/release/internal/dagger"
)

// Publish a Github release
func (r Release) githubRelease(
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
	// GitHub host for release creation
	// +optional
	githubHost string,
	// Additional CA certificate for the GitHub host
	// +optional
	githubCACert *dagger.File,
	// Whether to perform a dry run without creating the release
	// +optional
	dryRun bool,
) error {
	githubRepo, err := githubRepo(repository, githubHost)
	if err != nil {
		return err
	}
	if githubHost == "" {
		githubHost = "github.com"
	}

	if dryRun {
		// Check that the src commit is in the repo
		_, err = dag.
			Git(fmt.Sprintf("https://%s/%s", githubHost, githubRepo)).
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
		Repo:   githubRepo,
		Token:  token,
		Host:   githubHost,
		CaCert: githubCACert,
	})
	return gh.Release().Create(
		ctx,
		dest,
		dest,
		dagger.GhReleaseCreateOpts{
			Target:    src,
			NotesFile: notes,
			Latest:    dagger.GhLatestFalse,
		},
	)
}

func githubRepo(repo, githubHost string) (string, error) {
	u, err := url.Parse(repo)
	if err != nil {
		return "", err
	}
	if githubHost == "" {
		githubHost = "github.com"
	}
	if (u.Host != "") && (u.Host != githubHost) {
		return "", fmt.Errorf("git repo must be on %s", githubHost)
	}
	return strings.TrimPrefix(strings.TrimSuffix(u.Path, ".git"), "/"), nil
}
