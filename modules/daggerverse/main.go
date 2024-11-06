package main

import (
	"context"
	"dagger/daggerverse/internal/dagger"
	"fmt"
	"time"

	"github.com/google/go-github/v66/github"
)

type Daggerverse struct{}

// Deploy preview environment running Dagger main: dagger call deploy-preview-with-dagger-main --github-token=env:GITHUB_PAT
func (h *Daggerverse) DeployPreviewWithDaggerMain(
	ctx context.Context,
	githubToken *dagger.Secret,
) error {
	token, err := githubToken.Plaintext(ctx)
	if err != nil {
		return err
	}

	// get user config from githubToken
	ghc := github.NewClient(nil).WithAuthToken(token)
	user, _, err := ghc.Users.Get(ctx, "")
	if err != nil {
		return err
	}

	emails, _, err := ghc.Users.ListEmails(ctx, &github.ListOptions{})
	if err != nil {
		return err
	}

	today := time.Now()
	date := today.Format("2006-01-02")

	// clone dagger.io - private repository, requires a github token
	repo := "github.com/dagger/dagger.io"
	// clone dagger.io - private repository, requires a github token
	gh := dag.Gh(dagger.GhOpts{
		Token: githubToken,
		Repo:  repo,
	})

	// make a change so that a new Daggerverse deployment will be created
	daggerio := gh.Repo().Clone(
		repo,
		dagger.GhRepoCloneOpts{
			Args: []string{"--depth=1"},
		}).
		WithNewFile("daggerverse/CREATE_PREVIEW_ENVIRONMENT", today.String())

	branch := fmt.Sprintf("dgvs-test-with-dagger-main-%s", date)
	commitMsg := fmt.Sprintf(`dgvs: Test Dagger Engine main @ %s

daggerverse-checks in GitHub Actions ensures that module crawling works as expected. Should complete within 5 mins.`, date)

	// open a PR so that it creates a new Daggerverse preview environment running Dagger main
	err = gh.WithSource(daggerio).
		WithGitExec([]string{"checkout", "-b", branch}).
		WithGitExec([]string{"add", "daggerverse/CREATE_PREVIEW_ENVIRONMENT"}).
		WithGitExec([]string{"config", "user.email", *emails[0].Email}).
		WithGitExec([]string{"config", "user.name", *user.Name}).
		WithGitExec([]string{"commit", "-am", commitMsg}).
		WithGitExec([]string{"push", "--force", "origin", branch}).
		PullRequest().Create(
		ctx,
		dagger.GhPullRequestCreateOpts{
			Assignees: []string{*user.Login},
			Fill:      true,
			Labels:    []string{"preview", "area/daggerverse"},
			Head:      branch,
		},
	)

	return err
}
