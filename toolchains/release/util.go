package main

import (
	"context"
	"errors"
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

	exists, err := r.githubReleaseExists(ctx, repository, dest, src, token)
	if err != nil {
		return err
	}
	if exists {
		fmt.Printf("found existing GitHub release %s; skipping\n", dest)
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
			Latest:    dagger.GhLatestFalse,
		},
	)
}

func (r Release) githubReleaseExists(
	ctx context.Context,
	repository string,
	tag string,
	expectedCommit string,
	token *dagger.Secret,
) (bool, error) {
	githubRepo, err := githubRepo(repository)
	if err != nil {
		return false, err
	}

	gh := dag.Gh(dagger.GhOpts{
		Repo:  githubRepo,
		Token: token,
	})
	releaseExists := true
	if _, err = gh.Exec([]string{"release", "view", tag, "--json", "tagName"}).Sync(ctx); err != nil {
		if !isGitHubNotFound(err) {
			return false, err
		}
		releaseExists = false
	}

	if err := r.verifyGitHubReleaseTag(ctx, githubRepo, tag, expectedCommit, token); err != nil {
		return false, err
	}
	return releaseExists, nil
}

func (r Release) verifyGitHubReleaseTag(
	ctx context.Context,
	githubRepo string,
	tag string,
	expectedCommit string,
	token *dagger.Secret,
) error {
	gh := dag.Gh(dagger.GhOpts{
		Repo:  githubRepo,
		Token: token,
	})

	out, err := gh.Exec([]string{
		"api",
		fmt.Sprintf("repos/%s/git/ref/tags/%s", githubRepo, tag),
		"--jq",
		`.object.type + " " + .object.sha`,
	}).Stdout(ctx)
	if err != nil {
		if isGitHubNotFound(err) {
			return nil
		}
		return err
	}

	fields := strings.Fields(out)
	if len(fields) != 2 {
		return fmt.Errorf("unexpected GitHub tag response for %s: %q", tag, strings.TrimSpace(out))
	}

	kind, sha := fields[0], fields[1]
	if kind != "commit" {
		return fmt.Errorf("GitHub tag %s points to a %s object %s, expected commit %s", tag, kind, sha, expectedCommit)
	}

	if sha != expectedCommit {
		return fmt.Errorf("GitHub tag %s resolves to %s; expected %s", tag, sha, expectedCommit)
	}

	return nil
}

func isGitHubNotFound(err error) bool {
	var execErr *dagger.ExecError
	if errors.As(err, &execErr) {
		stderr := strings.ToLower(execErr.Stderr + "\n" + execErr.Stdout)
		if strings.Contains(stderr, "release not found") || strings.Contains(stderr, "not found") {
			return true
		}
	}

	return false
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
