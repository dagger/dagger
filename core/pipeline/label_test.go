package pipeline_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/stretchr/testify/require"
)

func TestLoadGitLabels(t *testing.T) {
	normalRepo := setupRepo(t)
	repoHead := run(t, "git", "-C", normalRepo, "rev-parse", "HEAD")

	detachedRepo := setupRepo(t)
	run(t, "git", "-C", detachedRepo, "commit", "--allow-empty", "-m", "second")
	run(t, "git", "-C", detachedRepo, "commit", "--allow-empty", "-m", "third")
	run(t, "git", "-C", detachedRepo, "checkout", "HEAD~2")
	run(t, "git", "-C", detachedRepo, "merge", "main")
	detachedHead := run(t, "git", "-C", detachedRepo, "rev-parse", "HEAD")

	type Example struct {
		Name   string
		Repo   string
		Labels []pipeline.Label
	}

	for _, example := range []Example{
		{
			Name: "normal branch state",
			Repo: normalRepo,
			Labels: []pipeline.Label{
				{
					Name:  "dagger.io/git.remote",
					Value: "example.com",
				},
				{
					Name:  "dagger.io/git.branch",
					Value: "main",
				},
				{
					Name:  "dagger.io/git.ref",
					Value: repoHead,
				},
				{
					Name:  "dagger.io/git.author.name",
					Value: "Test User",
				},
				{
					Name:  "dagger.io/git.author.email",
					Value: "test@example.com",
				},
				{
					Name:  "dagger.io/git.committer.name",
					Value: "Test User",
				},
				{
					Name:  "dagger.io/git.committer.email",
					Value: "test@example.com",
				},
				{
					Name:  "dagger.io/git.title",
					Value: "init",
				},
			},
		},
		{
			Name: "detached HEAD state",
			Repo: detachedRepo,
			Labels: []pipeline.Label{
				{
					Name:  "dagger.io/git.remote",
					Value: "example.com",
				},
				{
					Name:  "dagger.io/git.ref",
					Value: detachedHead,
				},
				{
					Name:  "dagger.io/git.author.name",
					Value: "Test User",
				},
				{
					Name:  "dagger.io/git.author.email",
					Value: "test@example.com",
				},
				{
					Name:  "dagger.io/git.committer.name",
					Value: "Test User",
				},
				{
					Name:  "dagger.io/git.committer.email",
					Value: "test@example.com",
				},
				{
					Name:  "dagger.io/git.title",
					Value: "third",
				},
			},
		},
	} {
		example := example
		t.Run(example.Name, func(t *testing.T) {
			labels, err := pipeline.LoadGitLabels(example.Repo)
			require.NoError(t, err)
			require.ElementsMatch(t, example.Labels, labels)
		})
	}
}

func TestLoadGitHubLabels(t *testing.T) {
	type Example struct {
		Name   string
		Env    []string
		Labels []pipeline.Label
	}

	for _, example := range []Example{
		{
			Name: "workflow_dispatch",
			Env: []string{
				"GITHUB_ACTIONS=true",
				"GITHUB_ACTOR=vito",
				"GITHUB_WORKFLOW=some-workflow",
				"GITHUB_JOB=some-job",
				"GITHUB_EVENT_NAME=workflow_dispatch",
				"GITHUB_EVENT_PATH=testdata/workflow_dispatch.json",
			},
			Labels: []pipeline.Label{
				{
					Name:  "github.com/actor",
					Value: "vito",
				},
				{
					Name:  "github.com/event.type",
					Value: "workflow_dispatch",
				},
				{
					Name:  "github.com/workflow.name",
					Value: "some-workflow",
				},
				{
					Name:  "github.com/workflow.job",
					Value: "some-job",
				},
				{
					Name:  "github.com/repo.full_name",
					Value: "dagger/testdata",
				},
				{
					Name:  "github.com/repo.url",
					Value: "https://github.com/dagger/testdata",
				},
			},
		},
		{
			Name: "pull_request.synchronize",
			Env: []string{
				"GITHUB_ACTIONS=true",
				"GITHUB_ACTOR=vito",
				"GITHUB_WORKFLOW=some-workflow",
				"GITHUB_JOB=some-job",
				"GITHUB_EVENT_NAME=pull_request",
				"GITHUB_EVENT_PATH=testdata/pull_request.synchronize.json",
			},
			Labels: []pipeline.Label{
				{
					Name:  "github.com/actor",
					Value: "vito",
				},
				{
					Name:  "github.com/event.type",
					Value: "pull_request",
				},
				{
					Name:  "github.com/workflow.name",
					Value: "some-workflow",
				},
				{
					Name:  "github.com/workflow.job",
					Value: "some-job",
				},
				{
					Name:  "github.com/event.action",
					Value: "synchronize",
				},
				{
					Name:  "github.com/repo.full_name",
					Value: "dagger/testdata",
				},
				{
					Name:  "github.com/repo.url",
					Value: "https://github.com/dagger/testdata",
				},
				{
					Name:  "github.com/pr.number",
					Value: "2018",
				},
				{
					Name:  "github.com/pr.title",
					Value: "dump env, use session binary from submodule",
				},
				{
					Name:  "github.com/pr.url",
					Value: "https://github.com/dagger/testdata/pull/2018",
				},
				{
					Name:  "github.com/pr.head",
					Value: "81be07d3103b512159628bfa3aae2fbb5d255964",
				},
				{
					Name:  "github.com/pr.branch",
					Value: "dump-env",
				},
				{
					Name:  "github.com/pr.label",
					Value: "vito:dump-env",
				},
			},
		},
		{
			Name: "push",
			Env: []string{
				"GITHUB_ACTIONS=true",
				"GITHUB_ACTOR=vito",
				"GITHUB_WORKFLOW=some-workflow",
				"GITHUB_JOB=some-job",
				"GITHUB_EVENT_NAME=push",
				"GITHUB_EVENT_PATH=testdata/push.json",
			},
			Labels: []pipeline.Label{
				{
					Name:  "github.com/actor",
					Value: "vito",
				},
				{
					Name:  "github.com/event.type",
					Value: "push",
				},
				{
					Name:  "github.com/workflow.name",
					Value: "some-workflow",
				},
				{
					Name:  "github.com/workflow.job",
					Value: "some-job",
				},
				{
					Name:  "github.com/repo.full_name",
					Value: "vito/bass",
				},
				{
					Name:  "github.com/repo.url",
					Value: "https://github.com/vito/bass",
				},
			},
		},
	} {
		example := example
		t.Run(example.Name, func(t *testing.T) {
			for _, e := range example.Env {
				k, v, _ := strings.Cut(e, "=")
				os.Setenv(k, v)
			}

			labels, err := pipeline.LoadGitHubLabels()
			require.NoError(t, err)
			require.ElementsMatch(t, example.Labels, labels)
		})
	}
}

func run(t *testing.T, exe string, args ...string) string { // nolint: unparam
	t.Helper()
	cmd := exec.Command(exe, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func setupRepo(t *testing.T) string {
	repo := t.TempDir()
	run(t, "git", "-C", repo, "init")
	run(t, "git", "-C", repo, "config", "--local", "--add", "user.name", "Test User")
	run(t, "git", "-C", repo, "config", "--local", "--add", "user.email", "test@example.com")
	run(t, "git", "-C", repo, "remote", "add", "origin", "https://example.com")
	run(t, "git", "-C", repo, "checkout", "-b", "main")
	run(t, "git", "-C", repo, "commit", "--allow-empty", "-m", "init")
	return repo
}
