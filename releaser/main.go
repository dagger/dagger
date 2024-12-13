// A module that encodes the official release process of the Dagger Engine
package main

import (
	"context"
	"dagger/releaser/internal/dagger"
	"fmt"
	"net/url"
	"strings"
)

type Releaser struct {
	ChangeNotesDir *dagger.Directory // +private
}

func New(
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!.changes/*.md", "!**/.changes/*.md"]
	changeNotesDir *dagger.Directory,
) Releaser {
	return Releaser{
		ChangeNotesDir: changeNotesDir,
	}
}

// Lookup the change notes file for the given component and version
func (r Releaser) ChangeNotes(
	ctx context.Context,
	// The component to look up change notes for
	// Example: "sdk/php"
	component,
	// The version to look up change notes for
	version string,
) (*dagger.File, error) {
	if version == "" {
		v, err := dag.Version().LastReleaseVersion(ctx)
		if err != nil {
			return nil, err
		}
		version = v
	}
	return r.ChangeNotesDir.File(fmt.Sprintf("%s/.changes/%s.md", component, version)), nil
}

// Publish a Github release
func (r Releaser) GithubRelease(
	ctx context.Context,
	// GitHub repository URL
	repository string,
	// Tag for the GitHub release
	// eg. v0.14.0
	tag string,
	// The target tag for the release
	// e.g. sdk/typescript/v0.14.0
	target string,
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

	commit, err := dag.Version().Git().Commit(target).Commit(ctx)
	if err != nil {
		return err
	}

	if dryRun {
		// Check that the target commit is in the target repo
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
		tag,
		tag,
		dagger.GhReleaseCreateOpts{
			Target:    commit,
			NotesFile: notes,
			Latest:    dagger.GhLatestLatestFalse,
		},
	)
}

func (r Releaser) Notify(
	ctx context.Context,
	// GitHub repository URL
	repository string,
	// The target tag for the release
	// e.g. sdk/typescript/v0.14.0
	target string,
	// Name of the component to release
	name string,
	// Discord webhook
	// +optional
	discordWebhook *dagger.Secret,

	// Whether to perform a dry run without creating the release
	// +optional
	dryRun bool,
) error {
	githubRepo, err := githubRepo(repository)
	if err != nil {
		return err
	}
	if dryRun {
		return nil
	}

	if discordWebhook != nil {
		message := fmt.Sprintf("%s: https://github.com/%s/releases/tag/%s", name, githubRepo, target)
		_, err = dag.Notify().Discord(ctx, discordWebhook, message)
		if err != nil {
			return err
		}
	}

	return nil
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
