package main

import (
	"context"
	"dagger/releaser/internal/dagger"
	"fmt"
	"net/url"
	"strings"
)

// Lookup the change notes file for the given component and version
func (r Releaser) changeNotes(
	// The component to look up change notes for
	// Example: "sdk/php"
	component,
	// The version to look up change notes for
	version string,
) *dagger.File {
	path := fmt.Sprintf(".changes/%s.md", version)
	if component != "" {
		path = strings.TrimSuffix(component, "/") + "/" + path
	}
	return r.Dagger.Source().File(path)
}

// Publish a GitHub release
func (r Releaser) githubRelease(
	ctx context.Context,
	// GitHub repository URL
	repository string,
	// Source tag for the GitHub release
	// eg. v0.14.0
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

	commit, err := dag.Version().Git().Commit(src).Commit(ctx)
	if err != nil {
		return err
	}

	if dryRun {
		// Check that the src commit is in the repo
		_, err = dag.
			Git(fmt.Sprintf("https://github.com/%s", githubRepo)).
			Commit(commit).
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
			Target:    commit,
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
