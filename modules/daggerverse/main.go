package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-github/v66/github"

	"dagger/daggerverse/internal/dagger"
)

type Daggerverse struct {
	// +private
	Gh *dagger.Gh
	// +private
	GitHubUser string
	// +private
	GitHubUserEmail string
	// +private
	Repo string
}

// +cache="session"
func New(
	ctx context.Context,
	// GitHub Personal Access Token which access to dagger/dagger.io repo
	githubToken *dagger.Secret,
) (*Daggerverse, error) {
	token, err := githubToken.Plaintext(ctx)
	if err != nil {
		return nil, err
	}

	// get user config from githubToken
	ghc := github.NewClient(nil).WithAuthToken(token)
	user, _, err := ghc.Users.Get(ctx, "")
	if err != nil {
		return nil, err
	}
	repo := "github.com/dagger/dagger.io"
	dgvs := &Daggerverse{
		GitHubUser: *user.Name,
		Repo:       repo,
		Gh: dag.Gh(dagger.GhOpts{
			Token: githubToken,
			Repo:  repo,
		}),
	}

	emails, _, err := ghc.Users.ListEmails(ctx, &github.ListOptions{})
	if err != nil {
		return nil, err
	}
	dgvs.GitHubUserEmail = *emails[0].Email

	return dgvs, nil
}

// Deploy preview environment running Dagger main: dagger call --github-token=env:GITHUB_PAT deploy-preview-with-dagger-main
// +cache="session"
func (h *Daggerverse) DeployPreviewWithDaggerMain(
	ctx context.Context,
	target string,

	// +optional
	githubAssignee string,
) error {
	// make a change so that a new Daggerverse deployment will be created
	daggerio := h.clone().
		WithNewFile("daggerverse/CREATE_PREVIEW_ENVIRONMENT", time.Now().String())

	branch := fmt.Sprintf("dgvs-test-with-dagger-main-%s", target)
	commitMsg := fmt.Sprintf(`dgvs: Test Dagger Engine main @ %s

daggerverse-checks in GitHub Actions ensures that module crawling works as expected. Should complete within 5 mins.

Triggered by %s.

		`, h.date(), fmt.Sprintf("https://github.com/dagger/dagger/pull/%s", target))

	// push the preview environment trigger branch
	gh := h.Gh.WithSource(daggerio).
		WithGitExec([]string{"checkout", "-b", branch}).
		WithGitExec([]string{"add", "daggerverse/CREATE_PREVIEW_ENVIRONMENT"}).
		WithGitExec([]string{"config", "user.email", h.GitHubUserEmail}).
		WithGitExec([]string{"config", "user.name", h.GitHubUser}).
		WithGitExec([]string{"commit", "-am", commitMsg}).
		WithGitExec([]string{"push", "-f", "origin", branch})
	if _, err := gh.Source().Sync(ctx); err != nil {
		return err
	}

	// open a PR on the trigger branch that it creates a new Daggerverse
	// preview environment running Dagger main
	exists, err := gh.PullRequest().Exists(ctx, branch)
	if err != nil {
		return err
	}
	if !exists {
		var assignees []string
		if githubAssignee != "" {
			assignees = append(assignees, githubAssignee)
		}
		err := gh.
			PullRequest().Create(
			ctx,
			dagger.GhPullRequestCreateOpts{
				Assignees: assignees,
				Fill:      true,
				Labels:    []string{"preview", "area/daggerverse"},
				Head:      branch,
			})
		if err != nil {
			return err
		}
	}

	return nil
}

func (h *Daggerverse) date() string {
	return time.Now().Format("2006-01-02")
}

func (h *Daggerverse) clone() *dagger.Directory {
	return h.Gh.Repo().Clone(
		h.Repo,
		dagger.GhRepoCloneOpts{
			Args: []string{"--depth=1"},
		})
}
