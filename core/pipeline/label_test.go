package pipeline_test

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestLoadClientLabels(t *testing.T) {
	labels := pipeline.LoadClientLabels(engine.Version)

	expected := []pipeline.Label{
		{"dagger.io/client.os", runtime.GOOS},
		{"dagger.io/client.arch", runtime.GOARCH},
		{"dagger.io/client.version", engine.Version},
	}

	require.ElementsMatch(t, expected, labels)
}

func TestLoadServerLabels(t *testing.T) {
	labels := pipeline.LoadServerLabels("0.8.4", "linux", "amd64", false)

	expected := []pipeline.Label{
		{"dagger.io/server.os", "linux"},
		{"dagger.io/server.arch", "amd64"},
		{"dagger.io/server.version", "0.8.4"},
		{"dagger.io/server.cache.enabled", "false"},
	}

	require.ElementsMatch(t, expected, labels)
}

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
					Name:  "dagger.io/git.branch",
					Value: "main",
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
					Name:  "dagger.io/vcs.triggerer.login",
					Value: "vito",
				},
				{
					Name:  "dagger.io/vcs.event.type",
					Value: "workflow_dispatch",
				},
				{
					Name:  "dagger.io/vcs.workflow.name",
					Value: "some-workflow",
				},
				{
					Name:  "dagger.io/vcs.job.name",
					Value: "some-job",
				},
				{
					Name:  "dagger.io/vcs.repo.full_name",
					Value: "dagger/testdata",
				},
				{
					Name:  "dagger.io/vcs.repo.url",
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
					Name:  "dagger.io/vcs.triggerer.login",
					Value: "vito",
				},
				{
					Name:  "dagger.io/vcs.event.type",
					Value: "pull_request",
				},
				{
					Name:  "dagger.io/vcs.workflow.name",
					Value: "some-workflow",
				},
				{
					Name:  "dagger.io/vcs.job.name",
					Value: "some-job",
				},
				{
					Name:  "github.com/event.action",
					Value: "synchronize",
				},
				{
					Name:  "dagger.io/vcs.repo.full_name",
					Value: "dagger/testdata",
				},
				{
					Name:  "dagger.io/vcs.repo.url",
					Value: "https://github.com/dagger/testdata",
				},
				{
					Name:  "dagger.io/vcs.change.number",
					Value: "2018",
				},
				{
					Name:  "dagger.io/vcs.change.title",
					Value: "dump env, use session binary from submodule",
				},
				{
					Name:  "dagger.io/vcs.change.url",
					Value: "https://github.com/dagger/testdata/pull/2018",
				},
				{
					Name:  "dagger.io/vcs.change.head_sha",
					Value: "81be07d3103b512159628bfa3aae2fbb5d255964",
				},
				{
					Name:  "dagger.io/vcs.change.branch",
					Value: "dump-env",
				},
				{
					Name:  "dagger.io/vcs.change.label",
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
					Name:  "dagger.io/vcs.triggerer.login",
					Value: "vito",
				},
				{
					Name:  "dagger.io/vcs.event.type",
					Value: "push",
				},
				{
					Name:  "dagger.io/vcs.workflow.name",
					Value: "some-workflow",
				},
				{
					Name:  "dagger.io/vcs.job.name",
					Value: "some-job",
				},
				{
					Name:  "dagger.io/vcs.repo.full_name",
					Value: "vito/bass",
				},
				{
					Name:  "dagger.io/vcs.repo.url",
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

func TestLoadGitLabLabels(t *testing.T) {
	type Example struct {
		Name   string
		Env    map[string]string
		Labels []pipeline.Label
	}

	for _, example := range []Example{
		{
			Name: "GitLab CI merge request job",
			Env: map[string]string{
				"GITLAB_CI":                           "true",
				"CI_PROJECT_URL":                      "https://gitlab.com/dagger/testdata",
				"CI_PROJECT_PATH":                     "dagger/testdata",
				"CI_MERGE_REQUEST_SOURCE_BRANCH_NAME": "feature-branch",
				"CI_MERGE_REQUEST_TITLE":              "Some title",
				"CI_MERGE_REQUEST_LABELS":             "label1,label2",
				"CI_COMMIT_SHA":                       "123abc",
				"CI_PIPELINE_SOURCE":                  "push",
				"CI_PIPELINE_NAME":                    "pipeline-name",
				"CI_JOB_ID":                           "123",
				"CI_JOB_NAME":                         "test-job",
				"GITLAB_USER_ID":                      "789",
				"GITLAB_USER_EMAIL":                   "user@gitlab.com",
				"GITLAB_USER_NAME":                    "Gitlab User",
				"GITLAB_USER_LOGIN":                   "gitlab-user",
			},
			Labels: []pipeline.Label{
				{Name: "dagger.io/vcs.repo.url", Value: "https://gitlab.com/dagger/testdata"},
				{Name: "dagger.io/vcs.repo.full_name", Value: "dagger/testdata"},
				{Name: "dagger.io/vcs.change.branch", Value: "feature-branch"},
				{Name: "dagger.io/vcs.change.title", Value: "Some title"},
				{Name: "dagger.io/vcs.change.head_sha", Value: "123abc"},
				{Name: "dagger.io/vcs.triggerer.login", Value: "gitlab-user"},
				{Name: "dagger.io/vcs.event.type", Value: "push"},
				{Name: "dagger.io/vcs.job.name", Value: "test-job"},
				{Name: "dagger.io/vcs.workflow.name", Value: "pipeline-name"},
				{Name: "dagger.io/vcs.change.label", Value: "label1,label2"},
				{Name: "gitlab.com/job.id", Value: "123"},
				{Name: "gitlab.com/triggerer.id", Value: "789"},
				{Name: "gitlab.com/triggerer.email", Value: "user@gitlab.com"},
				{Name: "gitlab.com/triggerer.name", Value: "Gitlab User"},
			},
		},
		{
			Name: "GitLab CI branch job",
			Env: map[string]string{
				"GITLAB_CI":          "true",
				"CI_PROJECT_URL":     "https://gitlab.com/dagger/testdata",
				"CI_PROJECT_PATH":    "dagger/testdata",
				"CI_COMMIT_BRANCH":   "feature-branch",
				"CI_COMMIT_TITLE":    "Some title",
				"CI_COMMIT_SHA":      "123abc",
				"CI_PIPELINE_SOURCE": "push",
				"CI_PIPELINE_NAME":   "pipeline-name",
				"CI_JOB_ID":          "123",
				"CI_JOB_NAME":        "test-job",
				"GITLAB_USER_ID":     "789",
				"GITLAB_USER_EMAIL":  "user@gitlab.com",
				"GITLAB_USER_NAME":   "Gitlab User",
				"GITLAB_USER_LOGIN":  "gitlab-user",
			},
			Labels: []pipeline.Label{
				{Name: "dagger.io/vcs.repo.url", Value: "https://gitlab.com/dagger/testdata"},
				{Name: "dagger.io/vcs.repo.full_name", Value: "dagger/testdata"},
				{Name: "dagger.io/vcs.change.branch", Value: "feature-branch"},
				{Name: "dagger.io/vcs.change.title", Value: "Some title"},
				{Name: "dagger.io/vcs.change.head_sha", Value: "123abc"},
				{Name: "dagger.io/vcs.triggerer.login", Value: "gitlab-user"},
				{Name: "dagger.io/vcs.event.type", Value: "push"},
				{Name: "dagger.io/vcs.job.name", Value: "test-job"},
				{Name: "dagger.io/vcs.workflow.name", Value: "pipeline-name"},
				{Name: "dagger.io/vcs.change.label", Value: ""},
				{Name: "gitlab.com/job.id", Value: "123"},
				{Name: "gitlab.com/triggerer.id", Value: "789"},
				{Name: "gitlab.com/triggerer.email", Value: "user@gitlab.com"},
				{Name: "gitlab.com/triggerer.name", Value: "Gitlab User"},
			},
		},
	} {
		example := example
		t.Run(example.Name, func(t *testing.T) {
			// Set environment variables
			for k, v := range example.Env {
				os.Setenv(k, v)
			}

			// Run the function and collect the result
			labels, err := pipeline.LoadGitLabLabels()

			// Clean up environment variables
			for k := range example.Env {
				os.Unsetenv(k)
			}

			// Make assertions
			require.NoError(t, err)
			require.ElementsMatch(t, example.Labels, labels)
		})
	}
}

func TestLoadCircleCILabels(t *testing.T) {
	type Example struct {
		Name   string
		Env    map[string]string
		Labels []pipeline.Label
	}

	for _, example := range []Example{
		{
			Name: "CircleCI",
			Env: map[string]string{
				"CIRCLECI":                      "true",
				"CIRCLE_BRANCH":                 "main",
				"CIRCLE_SHA1":                   "abc123",
				"CIRCLE_JOB":                    "build",
				"CIRCLE_PIPELINE_NUMBER":        "42",
				"CIRCLE_PIPELINE_TRIGGER_LOGIN": "circle-user",
				"CIRCLE_REPOSITORY_URL":         "git@github.com:user/repo.git",
				"CIRCLE_PROJECT_REPONAME":       "repo",
				"CIRCLE_PULL_REQUEST":           "https://github.com/circle/repo/pull/1",
			},
			Labels: []pipeline.Label{
				{Name: "dagger.io/vcs.change.branch", Value: "main"},
				{Name: "dagger.io/vcs.change.head_sha", Value: "abc123"},
				{Name: "dagger.io/vcs.job.name", Value: "build"},
				{Name: "dagger.io/vcs.change.number", Value: "42"},
				{Name: "dagger.io/vcs.triggerer.login", Value: "circle-user"},
				{Name: "dagger.io/vcs.repo.url", Value: "https://github.com/user/repo"},
				{Name: "dagger.io/vcs.repo.full_name", Value: "repo"},
				{Name: "dagger.io/vcs.change.url", Value: "https://github.com/circle/repo/pull/1"},
			},
		},
	} {
		example := example
		t.Run(example.Name, func(t *testing.T) {
			// Set environment variables
			for k, v := range example.Env {
				os.Setenv(k, v)
			}

			// Run the function and collect the result
			labels, err := pipeline.LoadCircleCILabels()

			// Clean up environment variables
			for k := range example.Env {
				os.Unsetenv(k)
			}

			// Make assertions
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
