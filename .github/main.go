package main

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/.github/internal/dagger"
)

const (
	daggerVersion      = "v0.13.7"
	upstreamRepository = "dagger/dagger"
	defaultRunner      = "ubuntu-latest"
	publicToken        = "dag_dagger_sBIv6DsjNerWvTqt2bSFeigBUqWxp9bhh3ONSSgeFnw"
	timeoutMinutes     = 10
)

type CI struct {
	// Workflows collects all the workflows together, and applies some defaults
	// that can apply across all of our different specific runners
	// +private
	Workflows *dagger.Gha

	// DaggerRunner is a set of utility helpers that provides defaults for all
	// of our general checks.
	// +private
	DaggerRunner *dagger.Gha
}

func New() *CI {
	ci := &CI{
		Workflows: dag.Gha(dagger.GhaOpts{
			JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
				PublicToken:   publicToken,
				DaggerVersion: daggerVersion,
			}),
		}),

		DaggerRunner: dag.Gha(dagger.GhaOpts{
			JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
				Runner:         NewDaggerRunner(daggerVersion).Medium().Cached().RunsOn(),
				TimeoutMinutes: timeoutMinutes,
			}),
			WorkflowDefaults: dag.Gha().Workflow("", dagger.GhaWorkflowOpts{
				PullRequestConcurrency:      "preempt",
				Permissions:                 []dagger.GhaPermission{dagger.GhaPermissionReadContents},
				OnPushBranches:              []string{"main"},
				OnPullRequestOpened:         true,
				OnPullRequestReopened:       true,
				OnPullRequestSynchronize:    true,
				OnPullRequestReadyForReview: true,
			}),
		}),
	}

	return ci.
		withModuleWorkflow(
			ci.DaggerRunner,
			".github",
			"Github",
			"check",
		).
		withWorkflow(
			ci.DaggerRunner,
			false,
			"Docs",
			"docs lint",
		).
		withSDKWorkflows(
			ci.DaggerRunner,
			ci.DaggerRunner.Workflow("SDKs"),
			NewDaggerRunner(daggerVersion),
		).
		withNightlyWorkflows().
		withPrepareReleaseWorkflow(NewDaggerRunner(daggerVersion))
}

// Generate Github Actions workflows to call our Dagger workflows
func (ci *CI) Generate(
	// +defaultPath="/"
	// +ignore=["*", "!.github"]
	repository *dagger.Directory,
) *dagger.Directory {
	return ci.Workflows.Generate(dagger.GhaGenerateOpts{
		Directory: repository,
	})
}

func (ci *CI) Check(ctx context.Context,
	// +defaultPath="/"
	// +ignore=["*", "!.github"]
	repository *dagger.Directory,
) error {
	return dag.Dirdiff().AssertEqual(ctx, repository, ci.Generate(repository), []string{".github/workflows"})
}

// Add a workflow with our project-specific defaults
func (ci *CI) withWorkflow(runner *dagger.Gha, devEngine bool, name string, command string) *CI {
	jobOpts := dagger.GhaJobOpts{}
	if devEngine {
		jobOpts.DaggerVersion = "."
	}
	w := runner.
		Workflow(name).
		WithJob(runner.Job(name, daggerCommand(command), jobOpts))

	ci.Workflows = ci.Workflows.WithWorkflow(w)
	return ci
}

// Add a general workflow
func (ci *CI) withModuleWorkflow(runner *dagger.Gha, module string, name string, command string) *CI {
	w := runner.
		Workflow(name).
		WithJob(runner.Job(name, command, dagger.GhaJobOpts{
			Module: module,
		}))

	ci.Workflows = ci.Workflows.WithWorkflow(w)
	return ci
}

func (ci *CI) withSDKWorkflows(runner *dagger.Gha, workflow *dagger.GhaWorkflow, compute Runner) *CI {
	workflow = workflow.
		WithJob(runner.Job("python", daggerCommand("check --targets=sdk/python"), dagger.GhaJobOpts{
			Runner: compute.Medium().Cached().RunsOn(),
		})).
		WithJob(runner.Job("python-dev", daggerCommand("check --targets=sdk/python"), dagger.GhaJobOpts{
			DaggerVersion: ".",
			Runner:        compute.Large().DaggerInDocker().RunsOn(),
		})).
		WithJob(runner.Job("typescript", daggerCommand("check --targets=sdk/typescript"), dagger.GhaJobOpts{
			Runner: compute.Medium().Cached().RunsOn(),
		})).
		WithJob(runner.Job("typescript-dev", daggerCommand("check --targets=sdk/typescript"), dagger.GhaJobOpts{
			DaggerVersion: ".",
			Runner:        compute.Large().DaggerInDocker().RunsOn(),
		})).
		WithJob(runner.Job("go", daggerCommand("check --targets=sdk/go"), dagger.GhaJobOpts{
			Runner: compute.Medium().Cached().RunsOn(),
		})).
		WithJob(runner.Job("go-dev", daggerCommand("check --targets=sdk/go"), dagger.GhaJobOpts{
			DaggerVersion: ".",
			Runner:        compute.Large().DaggerInDocker().RunsOn(),
		})).
		WithJob(runner.Job("java", daggerCommand("check --targets=sdk/java"), dagger.GhaJobOpts{
			Runner: compute.Medium().Cached().RunsOn(),
		})).
		WithJob(runner.Job("java-dev", daggerCommand("check --targets=sdk/java"), dagger.GhaJobOpts{
			DaggerVersion: ".",
			Runner:        compute.Large().DaggerInDocker().RunsOn(),
		})).
		WithJob(runner.Job("elixir", daggerCommand("check --targets=sdk/elixir"), dagger.GhaJobOpts{
			Runner: compute.XLarge().Cached().RunsOn(),
		})).
		WithJob(runner.Job("elixir-dev", daggerCommand("check --targets=sdk/elixir"), dagger.GhaJobOpts{
			DaggerVersion: ".",
			Runner:        compute.XLarge().DaggerInDocker().RunsOn(),
		})).
		WithJob(runner.Job("rust", daggerCommand("check --targets=sdk/rust"), dagger.GhaJobOpts{
			Runner:         compute.XLarge().Cached().RunsOn(),
			TimeoutMinutes: 15,
		})).
		WithJob(runner.Job("rust-dev", daggerCommand("check --targets=sdk/rust"), dagger.GhaJobOpts{
			DaggerVersion:  ".",
			Runner:         compute.XLarge().DaggerInDocker().RunsOn(),
			TimeoutMinutes: 15,
		})).
		WithJob(runner.Job("php", daggerCommand("check --targets=sdk/php"), dagger.GhaJobOpts{
			Runner: compute.Medium().Cached().RunsOn(),
		})).
		WithJob(runner.Job("php-dev", daggerCommand("check --targets=sdk/php"), dagger.GhaJobOpts{
			DaggerVersion: ".",
			Runner:        compute.Large().DaggerInDocker().RunsOn(),
		}))
	ci.Workflows = ci.Workflows.WithWorkflow(workflow)

	return ci
}

func (ci *CI) withNightlyWorkflows() *CI {
	runner := dag.Gha(dagger.GhaOpts{
		JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
			RunIf: fmt.Sprintf("${{ github.repository == '%s' }}", upstreamRepository),
		}),
		WorkflowDefaults: dag.Gha().Workflow("", dagger.GhaWorkflowOpts{
			PullRequestConcurrency: "preempt",
			Permissions:            []dagger.GhaPermission{dagger.GhaPermissionReadContents},
			OnSchedule:             []string{"6 0 * * *"},
		}),
	})
	computeVariants := []Runner{
		NewDepotRunner(daggerVersion),
		NewNamespaceRunner(daggerVersion).
			AddLabel("nscloud-cache-size-100gb").
			AddLabel("nscloud-exp-container-image-cache"),
	}

	for _, compute := range computeVariants {
		ci = ci.withSDKWorkflows(
			ci.DaggerRunner,
			runner.Workflow(compute.Pipeline("SDKs")),
			compute)
	}

	return ci
}

func (ci *CI) withPrepareReleaseWorkflow(compute Runner) *CI {
	gha := dag.Gha(dagger.GhaOpts{
		JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
			Runner:         compute.Medium().Cached().RunsOn(),
			DaggerVersion:  daggerVersion,
			TimeoutMinutes: timeoutMinutes,
		}),
		WorkflowDefaults: dag.Gha().Workflow("", dagger.GhaWorkflowOpts{
			PullRequestConcurrency: "queue",
			Permissions:            []dagger.GhaPermission{dagger.GhaPermissionReadContents},
			OnPullRequestOpened:    true,
			OnPullRequestPaths:     []string{"/CHANGELOG.md", "/.changes"},
		}),
	})
	w := gha.
		Workflow("daggerverse-preview").
		WithJob(gha.Job(
			"deploy",
			daggerCommand("deploy-preview-with-dagger-main --github-token=env:DAGGER_CI_GITHUB_TOKEN"),
			dagger.GhaJobOpts{
				Secrets: []string{"DAGGER_CI_GITHUB_TOKEN"},
			}))

	ci.Workflows = ci.Workflows.WithWorkflow(w)

	return ci
}

func daggerCommand(command string) string {
	return fmt.Sprintf(`--docker-cfg=file:$HOME/.docker/config.json %s`, command)
}
