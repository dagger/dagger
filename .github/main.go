package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/.github/internal/dagger"
)

const (
	daggerVersion      = "v0.13.5"
	upstreamRepository = "dagger/dagger"
	defaultRunner      = "ubuntu-latest"
	publicToken        = "dag_dagger_sBIv6DsjNerWvTqt2bSFeigBUqWxp9bhh3ONSSgeFnw"
	timeoutMinutes     = 10
)

type CI struct {
	// +private
	Repo *dagger.GhaRepository
}

func New() *CI {
	ci := &CI{
		Repo: dag.Gha().Repository(dagger.GhaRepositoryOpts{
			JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
				PublicToken:    publicToken,
				DaggerVersion:  daggerVersion,
				Runner:         []string{BronzeRunner(false)},
				TimeoutMinutes: timeoutMinutes,
			}),
			WorkflowDefaults: dag.Gha().Workflow("", dagger.GhaWorkflowOpts{
				PullRequestConcurrency:      "preempt",
				Permissions:                 []dagger.GhaPermission{dagger.ReadContents},
				OnPushBranches:              []string{"main"},
				OnPullRequestOpened:         true,
				OnPullRequestReopened:       true,
				OnPullRequestSynchronize:    true,
				OnPullRequestReadyForReview: true,
			}),
		}),
	}
	return ci.
		WithModuleWorkflow(
			".github",
			"Github",
			"check",
			nil,
		).
		WithWorkflow(
			"Docs",
			"docs lint",
			nil,
			false,
		).
		WithSdkWorkflows(
			"SDKs",
			"python",
			"typescript",
			"go",
			"java",
			"elixir",
			"rust",
			"php",
		)
}

// Generate Github Actions workflows to call our Dagger workflows
func (ci *CI) Generate(
	// +defaultPath="/"
	// +ignore=["*", "!.github"]
	repository *dagger.Directory,
) *dagger.Directory {
	return ci.Repo.Generate(dagger.GhaRepositoryGenerateOpts{
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
func (ci *CI) WithWorkflow(
	// Workflow name
	name string,
	// Workflow command
	command string,
	// +optional
	runner []string,
	// Build the local engine source, and run the workflow with it
	// +optional
	devEngine bool,
) *CI {
	jobOpts := dagger.GhaJobOpts{}
	if devEngine {
		jobOpts.DaggerVersion = "."
	}
	if len(runner) != 0 {
		jobOpts.Runner = runner
	}

	ci.Repo = ci.Repo.WithWorkflow(
		dag.Gha().
			Workflow(name).
			WithJob(dag.Gha().Job(name, daggerCommand(command), jobOpts)),
	)

	return ci
}

// Add a general workflow
func (ci *CI) WithModuleWorkflow(
	// Workflow module
	module string,
	// Workflow name
	name string,
	// Workflow command
	command string,
	// +optional
	runner []string,
) *CI {
	jobOpts := dagger.GhaJobOpts{
		Module: module,
	}
	if len(runner) != 0 {
		jobOpts.Runner = runner
	}

	ci.Repo = ci.Repo.WithWorkflow(
		dag.Gha().
			Workflow(name).
			WithJob(dag.Gha().Job(name, command, jobOpts)),
	)

	return ci
}

func (ci *CI) WithSdkWorkflows(name string, sdks ...string) *CI {
	w := dag.Gha().Workflow(name)

	for _, sdk := range sdks {
		command := daggerCommand("check --targets=sdk/" + sdk)
		w = w.
			WithJob(dag.Gha().Job(sdk, command)).
			WithJob(dag.Gha().Job(sdk+"-dev", command, dagger.GhaJobOpts{
				DaggerVersion: ".",
				Runner:        []string{SilverRunner(true)},
			}))
	}

	ci.Repo = ci.Repo.WithWorkflow(w)
	return ci
}

func daggerCommand(command string) string {
	return fmt.Sprintf(`--docker-cfg=file:$HOME/.docker/config.json %s`, command)
}

// Assemble a runner name for a workflow
func Runner(
	generation int,
	daggerVersion string,
	cpus int,
	singleTenant bool,
	dind bool,
) string {
	runner := fmt.Sprintf(
		"dagger-g%d-%s-%dc",
		generation,
		strings.ReplaceAll(daggerVersion, ".", "-"),
		cpus)
	if dind {
		runner += "-dind"
	}
	if singleTenant {
		runner += "-st"
	}

	// Fall back to default runner if repository is not upstream
	// (this is GHA DSL and will be evaluated by the GHA runner)
	return fmt.Sprintf(
		"${{ github.repository == '%s' && '%s' || '%s' }}",
		upstreamRepository,
		runner,
		defaultRunner,
	)
}

// Bronze runner: Multi-tenant instance, 4 cpu
func BronzeRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) string {
	return Runner(2, daggerVersion, 4, false, dind)
}

// Silver runner: Multi-tenant instance, 8 cpu
func SilverRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) string {
	return Runner(2, daggerVersion, 8, false, dind)
}

// Gold runner: Single-tenant instance, 16 cpu
func GoldRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) string {
	return Runner(2, daggerVersion, 16, true, dind)
}

// Platinum runner: Single-tenant instance, 32 cpu
func PlatinumRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) string {
	return Runner(2, daggerVersion, 32, true, dind)
}
