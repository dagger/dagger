package main

import (
	"context"
	"fmt"
	"strings"
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

daggerverse-checks in GitHub Actions ensures that module crawling works as expected. Should complete within 5 mins.`, h.date())

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

// Bump Dagger version: dagger call --github-token=env:GITHUB_PAT bump-dagger-version --from=0.13.7 --to=0.14.0
func (h *Daggerverse) BumpDaggerVersion(
	ctx context.Context,
	// +defaultPath="../../.changes"
	releases *dagger.Directory,
	// Which version of Dagger are we bumping from - defaults to version n-1
	// +optional
	from string,
	// Which version of Dagger are we bumping to
	to string,

	// +optional
	githubAssignee string,
) (err error) {
	if from == "" {
		from, err = dag.Container().From("alpine").
			WithDirectory("/releases", releases).
			WithWorkdir("/releases").
			WithExec([]string{"sh", "-c", "ls v* | awk -F'[v.]' '{ print $2\".\"$3.\".\"$4 }' | sort -V | tail -n 2 | head -n 1"}).
			Stdout(ctx)
		if err != nil {
			return err
		}
		from = strings.TrimSpace(from)
	}

	// get just the version, without the v semver prefix
	from = strings.TrimPrefix(from, "v")
	to = strings.TrimPrefix(to, "v")

	fromDashed := strings.ReplaceAll(from, ".", "-")
	toDashed := strings.ReplaceAll(to, ".", "-")
	engineImage := fmt.Sprintf("registry.dagger.io/engine:v%s", to)

	engine := dag.Container().From(engineImage).
		WithExposedPort(1234).
		WithDefaultArgs([]string{
			"--addr", "tcp://0.0.0.0:1234",
			"--network-cidr", "10.12.34.0/24",
		}).AsService(dagger.ContainerAsServiceOpts{InsecureRootCapabilities: true, UseEntrypoint: true})

	daggerio, err := dag.Container().From(engineImage).
		WithDirectory("/dagger.io", h.clone()).
		WithWorkdir("/dagger.io").
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("find .github/workflows -name '*daggerverse*' -exec sed -i 's/%s/%s/g' {} +", fromDashed, toDashed),
		}).
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("sed -i 's/\\(DaggerVersion\\s*=\\s*\"\\)%s\"/\\1%s\"/' daggerverse/dag/main.go", from, to),
		}).
		WithServiceBinding("daggerverse-engine", engine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://daggerverse-engine:1234").
		WithExec([]string{"nc", "-vzw", "1", "daggerverse-engine", "1234"}).
		WithExec([]string{"dagger", "version"}).
		WithExec([]string{"dagger", "core", "version"}).
		WithExec([]string{"dagger", "--mod=daggerverse/dag", "develop"}).
		WithExec([]string{"dagger", "--mod=daggerverse", "develop"}).
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("sed -i 's/v[0-9][0-9]*\\.[0-9][0-9]*\\.[0-9][0-9]*/v%s/' infra/ci/*/argocd/daggerverse-preview/appset.yaml", to),
		}).
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("sed -i 's/registry\\.dagger\\.io\\/engine:v[0-9][0-9]*\\.[0-9][0-9]*\\.[0-9][0-9]*/registry\\.dagger\\.io\\/engine:v%s/' infra/ci/*/argocd/daggerverse-preview/manifests/deployment.base.yaml", to),
		}).
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("sed -i 's/registry\\.dagger\\.io\\/engine:v[0-9][0-9]*\\.[0-9][0-9]*\\.[0-9][0-9]*/registry\\.dagger\\.io\\/engine:v%s/' infra/prod/*/argocd/daggerverse/deployment.yaml", to),
		}).
		Sync(ctx)
	if err != nil {
		return err
	}

	daggerverse := dag.Go(daggerio.Directory("daggerverse")).Env().
		WithExec([]string{"go", "get", fmt.Sprintf("dagger.io/dagger@v%s", to)}).
		WithExec([]string{"go", "mod", "tidy"})
	updated := daggerio.WithDirectory("daggerverse", daggerverse.Directory("."))

	branch := fmt.Sprintf("dgvs-bump-dagger-from-%s-to-%s-with-dagger-main", from, to)
	commitMsg := fmt.Sprintf("dgvs: Bump Dagger from %s to %s", from, to)

	var assignees []string
	if githubAssignee != "" {
		assignees = append(assignees, githubAssignee)
	}
	err = h.Gh.WithSource(updated.Directory(".")).
		WithGitExec([]string{"checkout", "-b", branch}).
		WithGitExec([]string{"add", ".github", "daggerverse", "infra"}).
		WithGitExec([]string{"config", "user.email", h.GitHubUserEmail}).
		WithGitExec([]string{"config", "user.name", h.GitHubUser}).
		WithGitExec([]string{"commit", "-am", commitMsg}).
		WithGitExec([]string{"push", "-f", "origin", branch}).
		PullRequest().Create(
		ctx,
		dagger.GhPullRequestCreateOpts{
			Assignees: assignees,
			Fill:      true,
			Labels:    []string{"preview", "area/daggerverse"},
			Head:      branch,
		},
	)

	return err
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
