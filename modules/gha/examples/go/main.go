package main

import (
	"github.com/dagger/dagger/modules/gha/examples/go/internal/dagger"
)

type Examples struct{}

// Access Github secrets
func (m *Examples) GhaSecrets() *dagger.Directory {
	return dag.
		Gha().
		WithPipeline(
			"deploy docs",
			"deploy-docs --source=. --password env:$DOCS_SERVER_PASSWORD",
			dagger.GhaWithPipelineOpts{
				Secrets: []string{"DOCS_SERVER_PASSWORD"},
			}).
		Config()
}

// Limit per-PR concurrency for expensive test pipelines
func (m *Examples) GhaConcurrency() *dagger.Directory {
	return dag.
		Gha().
		WithPipeline(
			"Giant test suite",
			"test --all",
			dagger.GhaWithPipelineOpts{
				PullRequestConcurrency: "preempt",
			},
		).
		Config()
}

// Access github context information magically injected as env variables
func (m *Examples) GhaGithubContext() *dagger.Directory {
	return dag.
		Gha().
		WithPipeline(
			"lint all branches",
			"lint --source=${GITHUB_REPOSITORY_URL}#${GITHUB_REF}",
			dagger.GhaWithPipelineOpts{
				OnPush: true,
			},
		).
		Config()
}

// Compose a pipeline from an external module, instead of the one embedded in the repo.
func (m *Examples) GhaCustomModule() *dagger.Directory {
	return dag.
		Gha().
		WithPipeline(
			"say hello",
			"hello --name=$GITHUB_REPOSITORY_OWNER",
			dagger.GhaWithPipelineOpts{
				Module: "github.com/shykes/hello",
			}).
		Config()
}

// Build and publish a container on push
func (m *Examples) GhaOnPush() *dagger.Directory {
	return dag.
		Gha().
		WithPipeline(
			"build and publish app container from main",
			"publish --source=. --registry-user=$REGISTRY_USER --registry-password=$REGISTRY_PASSWORD",
			dagger.GhaWithPipelineOpts{
				OnPushBranches: []string{"main"},
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
		WithPipeline(
			"test pull requests",
			"test --all --source=.",
			dagger.GhaWithPipelineOpts{
				OnPullRequestOpened:      true,
				OnPullRequestSynchronize: true,
			},
		).
		Config()
}
