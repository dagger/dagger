package main

import (
	"github.com/dagger/dagger/modules/gha/examples/go/internal/dagger"
)

type Examples struct{}

// Access Github secrets
func (m *Examples) GhaSecrets() *dagger.Directory {
	return dag.
		Gha().
		Pipeline("deploy docs").
		WithJob(
			"deploy-docs",
			"deploy-docs --source=. --password env:$DOCS_SERVER_PASSWORD",
			dagger.GhaPipelineWithJobOpts{
				Secrets: []string{"DOCS_SERVER_PASSWORD"},
			},
		).
		Config()
}

// Limit per-PR concurrency for expensive test pipelines
func (m *Examples) GhaConcurrency() *dagger.Directory {
	return dag.
		Gha().
		Pipeline(
			"Giant test suite",
			dagger.GhaPipelineOpts{
				PullRequestConcurrency: "preempt",
			}).
		WithJob(
			"test",
			"test --all",
		).
		Config()
}

// Access github context information magically injected as env variables
func (m *Examples) GhaGithubContext() *dagger.Directory {
	return dag.
		Gha().
		Pipeline(
			"lint all branches",
			dagger.GhaPipelineOpts{
				OnPush: true,
			}).
		WithJob("lin", "lint --source=${GITHUB_REPOSITORY_URL}#${GITHUB_REF}").
		Config()
}

// Compose a pipeline from an external module, instead of the one embedded in the repo.
func (m *Examples) GhaCustomModule() *dagger.Directory {
	return dag.
		Gha().
		Pipeline("say hello").
		WithJob(
			"hello",
			"hello --name=$GITHUB_REPOSITORY_OWNER",
			dagger.GhaPipelineWithJobOpts{
				Module: "github.com/shykes/hello",
			}).
		Config()
}

// Build and publish a container on push
func (m *Examples) GhaOnPush() *dagger.Directory {
	return dag.
		Gha().
		Pipeline(
			"build and publish app container from main",
			dagger.GhaPipelineOpts{
				OnPushBranches: []string{"main"},
			}).
		WithJob(
			"publish",
			"publish --source=. --registry-user=$REGISTRY_USER --registry-password=$REGISTRY_PASSWORD",
			dagger.GhaPipelineWithJobOpts{
				Secrets: []string{
					"REGISTRY_USER", "REGISTRY_PASSWORD",
				},
			}).
		Config()
}

// Call integration tests on pull requests
func (m *Examples) GhaOnPullRequest() *dagger.Directory {
	return dag.
		Gha().
		Pipeline(
			"test pull requests",
			dagger.GhaPipelineOpts{
				OnPullRequestOpened:      true,
				OnPullRequestSynchronize: true,
			}).
		WithJob("test", "test --all --source=.").
		Config()
}
