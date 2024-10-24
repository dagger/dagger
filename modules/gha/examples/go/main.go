package main

import (
	"github.com/dagger/dagger/modules/gha/examples/go/internal/dagger"
)

type Examples struct{}

// Access Github secrets
func (m *Examples) GhaSecrets() *dagger.Directory {
	return dag.
		Gha().
		Repository().
		WithWorkflow(
			dag.Gha().Workflow("deploy docs").
				WithJob(dag.Gha().Job(
					"deploy-docs",
					"deploy-docs --source=. --password env:$DOCS_SERVER_PASSWORD",
					dagger.GhaJobOpts{
						Secrets: []string{"DOCS_SERVER_PASSWORD"},
					}),
				),
		).
		Generate()
}

// Limit per-PR concurrency for expensive test pipelines
func (m *Examples) GhaConcurrency() *dagger.Directory {
	return dag.
		Gha().
		Repository().
		WithWorkflow(
			dag.Gha().Workflow("Giant test suite", dagger.GhaWorkflowOpts{
				PullRequestConcurrency: "preempt",
			}).
				WithJob(dag.Gha().Job("test", "test --all")),
		).
		Generate()
}

// Access github context information magically injected as env variables
func (m *Examples) GhaGithubContext() *dagger.Directory {
	return dag.
		Gha().
		Repository().
		WithWorkflow(
			dag.Gha().Workflow("lint all branches", dagger.GhaWorkflowOpts{
				OnPush: true,
			}).
				WithJob(dag.Gha().Job("lin", "lint --source=${GITHUB_REPOSITORY_URL}#${GITHUB_REF}")),
		).
		Generate()
}

// Compose a pipeline from an external module, instead of the one embedded in the repo.
func (m *Examples) GhaCustomModule() *dagger.Directory {
	return dag.
		Gha().
		Repository().
		WithWorkflow(
			dag.Gha().Workflow("say hello").
				WithJob(dag.Gha().Job("hello", "hello --name=$GITHUB_REPOSITORY_OWNER", dagger.GhaJobOpts{
					Module: "github.com/shykes/hello",
				})),
		).
		Generate()
}

// Build and publish a container on push
func (m *Examples) GhaOnPush() *dagger.Directory {
	return dag.
		Gha().
		Repository().
		WithWorkflow(
			dag.Gha().Workflow(
				"build and publish app container from main",
				dagger.GhaWorkflowOpts{
					OnPushBranches: []string{"main"},
				}).
				WithJob(dag.Gha().Job(
					"publish",
					"publish --source=. --registry-user=$REGISTRY_USER --registry-password=$REGISTRY_PASSWORD",
					dagger.GhaJobOpts{
						Secrets: []string{
							"REGISTRY_USER", "REGISTRY_PASSWORD",
						},
					},
				)),
		).
		Generate()
}

// Call integration tests on pull requests
func (m *Examples) GhaOnPullRequest() *dagger.Directory {
	return dag.
		Gha().
		Repository().
		WithWorkflow(dag.Gha().Workflow(
			"test pull requests",
			dagger.GhaWorkflowOpts{
				OnPullRequestOpened:      true,
				OnPullRequestSynchronize: true,
			}).
			WithJob(dag.Gha().Job("test", "test --all --source=.")),
		).
		Generate()
}
