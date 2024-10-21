package main

import (
	"context"
	"dagger/daggerverse/internal/dagger"
	"time"
)

type Daggerverse struct{}

func (h *Daggerverse) CreatePrWithDaggerMain(
	ctx context.Context,
	githubToken *dagger.Secret,
	// TODO: set from the token
	prCreator string,
) error {
	repo := "github.com/dagger/dagger.io"
	// clone Dagger.io (will need a github token)
	gh := dag.Gh(dagger.GhOpts{
		Token: githubToken,
		Repo:  repo,
	})

	// make a change that will trigger a Daggerverse deploy
	daggerio := gh.Repo().Clone(
		repo,
		dagger.GhRepoCloneOpts{
			Args: []string{"--depth=1"},
		}).
		WithNewFile("daggerverse/TRIGGER_DEPLOY", time.Now().String())

	branch := "trigger-deploy-with-dagger-main"

	// open a PR with that change
	err := gh.WithSource(daggerio).
		WithGitExec([]string{"checkout", "-b", branch}).
		WithGitExec([]string{"add", "daggerverse/TRIGGER_DEPLOY"}).
		// TODO: configure from the token https://github.com/cli/cli/issues/6096#issuecomment-2402063775
		WithGitExec([]string{"config", "user.email", "foo@bar.com"}).
		WithGitExec([]string{"config", "user.name", "Foo"}).
		WithGitExec([]string{"commit", "-am", "'Trigger Daggerverse deploy with Dagger Engine main version'"}).
		WithGitExec([]string{"push", "--force", "origin", branch}).
		PullRequest().Create(
		ctx,
		dagger.GhPullRequestCreateOpts{
			Assignees: []string{prCreator},
			Fill:      true,
			Labels:    []string{"preview", "area/daggerverse"},
			Head:      branch,
		},
	)

	return err
}
