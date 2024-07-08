package telemetry_test

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetry"
)

func TestLoadClientLabels(t *testing.T) {
	labels := telemetry.Labels{}.WithClientLabels(engine.Version)

	expected := telemetry.Labels{
		"dagger.io/client.os":      runtime.GOOS,
		"dagger.io/client.arch":    runtime.GOARCH,
		"dagger.io/client.version": engine.Version,
	}

	require.Subset(t, labels, expected)
}

func TestLoadServerLabels(t *testing.T) {
	labels := telemetry.Labels{}.WithServerLabels("0.8.4", "linux", "amd64", false)

	expected := telemetry.Labels{
		"dagger.io/server.os":            "linux",
		"dagger.io/server.arch":          "amd64",
		"dagger.io/server.version":       "0.8.4",
		"dagger.io/server.cache.enabled": "false",
	}

	require.Subset(t, labels, expected)
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
		Labels telemetry.Labels
	}

	for _, example := range []Example{
		{
			Name: "normal branch state",
			Repo: normalRepo,
			Labels: telemetry.Labels{
				"dagger.io/git.remote":          "example.com",
				"dagger.io/git.branch":          "main",
				"dagger.io/git.ref":             repoHead,
				"dagger.io/git.author.name":     "Test User",
				"dagger.io/git.author.email":    "test@example.com",
				"dagger.io/git.committer.name":  "Test User",
				"dagger.io/git.committer.email": "test@example.com",
				"dagger.io/git.title":           "init",
			},
		},
		{
			Name: "detached HEAD state",
			Repo: detachedRepo,
			Labels: telemetry.Labels{
				"dagger.io/git.remote":          "example.com",
				"dagger.io/git.branch":          "main",
				"dagger.io/git.ref":             detachedHead,
				"dagger.io/git.author.name":     "Test User",
				"dagger.io/git.author.email":    "test@example.com",
				"dagger.io/git.committer.name":  "Test User",
				"dagger.io/git.committer.email": "test@example.com",
				"dagger.io/git.title":           "third",
			},
		},
	} {
		example := example
		t.Run(example.Name, func(t *testing.T) {
			labels := telemetry.Labels{}.WithGitLabels(example.Repo)
			require.Subset(t, labels, example.Labels)
		})
	}
}

func TestLoadGitRefEnvLabels(t *testing.T) {
	normalRepo := setupRepo(t)
	run(t, "git", "-C", normalRepo, "commit", "--allow-empty", "-m", "second")
	run(t, "git", "-C", normalRepo, "commit", "--allow-empty", "-m", "third")
	repoHead1 := run(t, "git", "-C", normalRepo, "rev-parse", "HEAD~")
	repoHead2 := run(t, "git", "-C", normalRepo, "rev-parse", "HEAD~~")
	run(t, "git", "-C", normalRepo, "update-ref", "refs/pull/1/head", repoHead2)
	run(t, "git", "-C", normalRepo, "update-ref", "refs/pull/1/merge", repoHead1)

	cloneRepo := cloneRepo(t, normalRepo)

	type Example struct {
		Name   string
		Repo   string
		Env    []string
		Labels telemetry.Labels
	}

	for _, example := range []Example{
		{
			Name: "normal branch state",
			Repo: cloneRepo,
			Env: []string{
				// GITHUB_REF overrides the git ref to point to this PR
				"GITHUB_REF=refs/pull/1/head",
			},
			Labels: telemetry.Labels{
				"dagger.io/git.ref": repoHead2,
			},
		},
		{
			Name: "normal branch state",
			Repo: cloneRepo,
			Env: []string{
				// same as above, but still use the /head
				"GITHUB_REF=refs/pull/1/merge",
			},
			Labels: telemetry.Labels{
				"dagger.io/git.ref": repoHead2,
			},
		},
	} {
		example := example
		t.Run(example.Name, func(t *testing.T) {
			for _, e := range example.Env {
				k, v, _ := strings.Cut(e, "=")
				t.Setenv(k, v)
			}

			labels := telemetry.Labels{}.WithGitLabels(example.Repo)
			require.Subset(t, labels, example.Labels)
		})
	}
}

func TestLoadGitHubLabels(t *testing.T) {
	type Example struct {
		Name   string
		Env    []string
		Labels telemetry.Labels
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
			Labels: telemetry.Labels{
				"dagger.io/vcs.triggerer.login": "vito",
				"dagger.io/vcs.event.type":      "workflow_dispatch",
				"dagger.io/vcs.workflow.name":   "some-workflow",
				"dagger.io/vcs.job.name":        "some-job",
				"dagger.io/vcs.repo.full_name":  "dagger/testdata",
				"dagger.io/vcs.repo.url":        "https://github.com/dagger/testdata",
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
			Labels: telemetry.Labels{
				"dagger.io/vcs.triggerer.login": "vito",
				"dagger.io/vcs.event.type":      "pull_request",
				"dagger.io/vcs.workflow.name":   "some-workflow",
				"dagger.io/vcs.job.name":        "some-job",
				"github.com/event.action":       "synchronize",
				"dagger.io/vcs.repo.full_name":  "dagger/testdata",
				"dagger.io/vcs.repo.url":        "https://github.com/dagger/testdata",
				"dagger.io/vcs.change.number":   "2018",
				"dagger.io/vcs.change.title":    "dump env, use session binary from submodule",
				"dagger.io/vcs.change.url":      "https://github.com/dagger/testdata/pull/2018",
				"dagger.io/vcs.change.head_sha": "81be07d3103b512159628bfa3aae2fbb5d255964",
				"dagger.io/vcs.change.branch":   "dump-env",
				"dagger.io/vcs.change.label":    "vito:dump-env",
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
			Labels: telemetry.Labels{
				"dagger.io/vcs.triggerer.login": "vito",
				"dagger.io/vcs.event.type":      "push",
				"dagger.io/vcs.workflow.name":   "some-workflow",
				"dagger.io/vcs.job.name":        "some-job",
				"dagger.io/vcs.repo.full_name":  "vito/bass",
				"dagger.io/vcs.repo.url":        "https://github.com/vito/bass",
			},
		},
	} {
		example := example
		t.Run(example.Name, func(t *testing.T) {
			for _, e := range example.Env {
				k, v, _ := strings.Cut(e, "=")
				t.Setenv(k, v)
			}

			labels := telemetry.Labels{}.WithGitHubLabels()
			require.Subset(t, labels, example.Labels)
		})
	}
}

func TestLoadGitLabLabels(t *testing.T) {
	type Example struct {
		Name   string
		Env    map[string]string
		Labels telemetry.Labels
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
			Labels: telemetry.Labels{
				"dagger.io/vcs.repo.url":        "https://gitlab.com/dagger/testdata",
				"dagger.io/vcs.repo.full_name":  "dagger/testdata",
				"dagger.io/vcs.change.branch":   "feature-branch",
				"dagger.io/vcs.change.title":    "Some title",
				"dagger.io/vcs.change.head_sha": "123abc",
				"dagger.io/vcs.triggerer.login": "gitlab-user",
				"dagger.io/vcs.event.type":      "push",
				"dagger.io/vcs.job.name":        "test-job",
				"dagger.io/vcs.workflow.name":   "pipeline-name",
				"dagger.io/vcs.change.label":    "label1,label2",
				"gitlab.com/job.id":             "123",
				"gitlab.com/triggerer.id":       "789",
				"gitlab.com/triggerer.email":    "user@gitlab.com",
				"gitlab.com/triggerer.name":     "Gitlab User",
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
			Labels: telemetry.Labels{
				"dagger.io/vcs.repo.url":        "https://gitlab.com/dagger/testdata",
				"dagger.io/vcs.repo.full_name":  "dagger/testdata",
				"dagger.io/vcs.change.branch":   "feature-branch",
				"dagger.io/vcs.change.title":    "Some title",
				"dagger.io/vcs.change.head_sha": "123abc",
				"dagger.io/vcs.triggerer.login": "gitlab-user",
				"dagger.io/vcs.event.type":      "push",
				"dagger.io/vcs.job.name":        "test-job",
				"dagger.io/vcs.workflow.name":   "pipeline-name",
				"dagger.io/vcs.change.label":    "",
				"gitlab.com/job.id":             "123",
				"gitlab.com/triggerer.id":       "789",
				"gitlab.com/triggerer.email":    "user@gitlab.com",
				"gitlab.com/triggerer.name":     "Gitlab User",
			},
		},
	} {
		example := example
		t.Run(example.Name, func(t *testing.T) {
			// Set environment variables
			for k, v := range example.Env {
				t.Setenv(k, v)
			}

			// Run the function and collect the result
			labels := telemetry.Labels{}.WithGitLabLabels()

			// Clean up environment variables
			for k := range example.Env {
				os.Unsetenv(k)
			}

			// Make assertions
			require.Subset(t, labels, example.Labels)
		})
	}
}

func TestLoadCircleCILabels(t *testing.T) {
	type Example struct {
		Name   string
		Env    map[string]string
		Labels telemetry.Labels
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
			Labels: telemetry.Labels{
				"dagger.io/vcs.change.branch":   "main",
				"dagger.io/vcs.change.head_sha": "abc123",
				"dagger.io/vcs.job.name":        "build",
				"dagger.io/vcs.change.number":   "42",
				"dagger.io/vcs.triggerer.login": "circle-user",
				"dagger.io/vcs.repo.url":        "https://github.com/user/repo",
				"dagger.io/vcs.repo.full_name":  "repo",
				"dagger.io/vcs.change.url":      "https://github.com/circle/repo/pull/1",
			},
		},
	} {
		example := example
		t.Run(example.Name, func(t *testing.T) {
			// Set environment variables
			for k, v := range example.Env {
				t.Setenv(k, v)
			}

			// Run the function and collect the result
			labels := telemetry.Labels{}.WithCircleCILabels()

			// Clean up environment variables
			for k := range example.Env {
				os.Unsetenv(k)
			}

			// Make assertions
			require.Subset(t, labels, example.Labels)
		})
	}
}

func TestLoadJenkinsLabels(t *testing.T) {
	type Example struct {
		Name   string
		Env    map[string]string
		Labels telemetry.Labels
	}

	for _, example := range []Example{
		{
			Name: "Jenkins",
			Env: map[string]string{
				"JENKINS_HOME": "/var/lib/jenkins",
				"GIT_BRANCH":   "origin/test-feature",
				"GIT_COMMIT":   "abc123",
			},
			Labels: telemetry.Labels{
				"dagger.io/git.branch":   "test-feature",
				"dagger.io/git.ref": "abc123",
			},
		},
	} {
		example := example
		t.Run(example.Name, func(t *testing.T) {
			// Set environment variables
			for k, v := range example.Env {
				t.Setenv(k, v)
			}

			// Run the function and collect the result
			labels := telemetry.Labels{}.WithJenkinsLabels()

			// Clean up environment variables
			for k := range example.Env {
				os.Unsetenv(k)
			}

			// Make assertions
			require.Subset(t, labels, example.Labels)
		})
	}
}

func run(t *testing.T, exe string, args ...string) string {
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

func cloneRepo(t *testing.T, src string) string {
	repo := t.TempDir()
	run(t, "git", "clone", "file://"+src, repo)
	return repo
}
