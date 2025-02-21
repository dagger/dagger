package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/.github/internal/dagger"
)

const (
	daggerVersion      = "v0.16.1"
	upstreamRepository = "dagger/dagger"
	defaultRunner      = "ubuntu-latest"
	publicToken        = "dag_dagger_sBIv6DsjNerWvTqt2bSFeigBUqWxp9bhh3ONSSgeFnw"
	timeoutMinutes     = 20
)

type CI struct {
	// Workflows collects all the workflows together, and applies some defaults
	// that can apply across all of our different specific runners
	// +private
	Workflows *dagger.Gha

	GithubRunner *dagger.Gha // +private
	DaggerRunner *dagger.Gha // +private
}

func New() *CI {
	workflow := dag.Gha().Workflow("", dagger.GhaWorkflowOpts{
		PullRequestConcurrency:      "preempt",
		Permissions:                 []dagger.GhaPermission{dagger.GhaPermissionReadContents},
		OnPushBranches:              []string{"main"},
		OnPullRequestOpened:         true,
		OnPullRequestReopened:       true,
		OnPullRequestSynchronize:    true,
		OnPullRequestReadyForReview: true,
	})

	ci := &CI{
		Workflows: dag.Gha(dagger.GhaOpts{
			JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
				PublicToken:   publicToken,
				DaggerVersion: daggerVersion,
			}),
		}),

		GithubRunner: dag.Gha(dagger.GhaOpts{
			JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
				Runner:         []string{"ubuntu-latest"},
				TimeoutMinutes: timeoutMinutes,
			}),
			WorkflowDefaults: workflow,
		}),
		DaggerRunner: dag.Gha(dagger.GhaOpts{
			JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
				Runner:         []string{BronzeRunner(false)},
				TimeoutMinutes: timeoutMinutes,
			}),
			WorkflowDefaults: workflow,
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
			"docs",
			"docs lint",
		).
		withWorkflow(
			ci.GithubRunner,
			false,
			"Helm",
			"check --targets=helm",
		).
		withTestWorkflows(
			ci.DaggerRunner,
			"Engine & CLI",
		).
		withSDKWorkflows(
			ci.DaggerRunner,
			"SDKs",
			"python",
			"typescript",
			"go",
			"java",
			"elixir",
			"rust",
			"php",
			"dotnet",
		).
		withPrepareReleaseWorkflow()
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

func (ci *CI) withSDKWorkflows(runner *dagger.Gha, name string, sdks ...string) *CI {
	w := runner.Workflow(name)
	for _, sdk := range sdks {
		command := daggerCommand("check --targets=sdk/" + sdk)
		w = w.
			WithJob(runner.Job(sdk, command)).
			WithJob(runner.Job(sdk+"-dev", command, dagger.GhaJobOpts{
				DaggerVersion: ".",
				Runner:        []string{SilverRunner(true)},
			}))
	}

	ci.Workflows = ci.Workflows.WithWorkflow(w)
	return ci
}

func (ci *CI) withTestWorkflows(runner *dagger.Gha, name string) *CI {
	w := runner.
		Workflow(name).
		WithJob(runner.Job("engine-lint", "engine lint", dagger.GhaJobOpts{
			Runner: []string{GoldRunner(false)},
		})).
		WithJob(runner.Job("scripts-lint", "scripts lint")).
		WithJob(runner.Job("cli-test-publish", "cli test-publish")).
		WithJob(runner.Job("engine-test-publish", "engine publish --image=dagger-engine.dev --tag=main --dry-run")).
		WithJob(runner.Job("scan-engine", "engine scan")).
		With(splitTests(runner, "test-", false, []testSplit{
			{"modules", []string{"TestModule"}, &dagger.GhaJobOpts{
				Runner: []string{GoldRunner(false)},
			}},
			{"module-runtimes", []string{"TestGo", "TestPython", "TestTypescript", "TestElixir", "TestPHP", "TestJava"}, &dagger.GhaJobOpts{
				Runner: []string{GoldRunner(false)},
			}},
			{"cli-engine", []string{"TestCLI", "TestEngine"}, &dagger.GhaJobOpts{
				Runner: []string{GoldRunner(false)},
			}},
			{"cgroupsv2", []string{"TestProvision", "TestTelemetry"}, &dagger.GhaJobOpts{
				// HACK: our main runners don't support cgroupsv2
				Runner: []string{"ubuntu-latest"},
				// NOTE: needed to silence redis warning logs in tests
				SetupCommands: []string{"sudo sysctl -w vm.overcommit_memory=1"},
			}},
			{"everything-else", nil, &dagger.GhaJobOpts{
				Runner: []string{PlatinumRunner(false)},
			}},
		})).
		With(splitTests(runner, "testdev-", true, []testSplit{
			{"modules", []string{"TestModule"}, &dagger.GhaJobOpts{
				Runner: []string{PlatinumRunner(true)},
			}},
			{"module-runtimes", []string{"TestGo", "TestPython", "TestTypescript", "TestElixir", "TestPHP", "TestJava"}, &dagger.GhaJobOpts{
				Runner: []string{PlatinumRunner(true)},
			}},
			{"container", []string{"TestContainer"}, &dagger.GhaJobOpts{
				Runner: []string{PlatinumRunner(true)},
			}},
		}))

	ci.Workflows = ci.Workflows.WithWorkflow(w)
	return ci
}

type testSplit struct {
	name  string
	tests []string
	// runner string
	opts *dagger.GhaJobOpts
}

// tests are temporarily split out - for context: https://github.com/dagger/dagger/pull/8998#issuecomment-2491426455
func splitTests(runner *dagger.Gha, name string, dev bool, splits []testSplit) dagger.WithGhaWorkflowFunc {
	return func(w *dagger.GhaWorkflow) *dagger.GhaWorkflow {
		var doneTests []string
		for _, split := range splits {
			command := "test specific --race=true --parallel=16 "
			if split.tests != nil {
				command += fmt.Sprintf("--run='%s'", strings.Join(split.tests, "|"))
			} else {
				command += fmt.Sprintf("--skip='%s'", strings.Join(doneTests, "|"))
			}
			doneTests = append(doneTests, split.tests...)

			opts := *split.opts
			opts.TimeoutMinutes = 30
			opts.UploadLogs = true
			if dev {
				opts.DaggerVersion = "."
			}
			w = w.WithJob(runner.Job(name+split.name, command, opts))
		}
		return w
	}
}

func (ci *CI) withPrepareReleaseWorkflow() *CI {
	gha := dag.Gha(dagger.GhaOpts{
		JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
			Runner:         []string{BronzeRunner(false)},
			DaggerVersion:  daggerVersion,
			TimeoutMinutes: timeoutMinutes,
		}),
		WorkflowDefaults: dag.Gha().Workflow("", dagger.GhaWorkflowOpts{
			PullRequestConcurrency:      "queue",
			Permissions:                 []dagger.GhaPermission{dagger.GhaPermissionReadContents},
			OnPullRequestOpened:         true,
			OnPullRequestReopened:       true,
			OnPullRequestSynchronize:    true,
			OnPullRequestReadyForReview: true,
			OnPullRequestPaths:          []string{".changes/v*.md"},
		}),
	})
	w := gha.
		Workflow("daggerverse-preview").
		WithJob(gha.Job(
			"deploy",
			"--github-token=env:RELEASE_DAGGER_CI_TOKEN deploy-preview-with-dagger-main --target $GITHUB_REF_NAME --github-assignee $GITHUB_ACTOR",
			dagger.GhaJobOpts{
				Secrets: []string{"RELEASE_DAGGER_CI_TOKEN"},
				Module:  "modules/daggerverse",
			}))

	ci.Workflows = ci.Workflows.WithWorkflow(w)

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
	return Runner(3, daggerVersion, 4, false, dind)
}

// Silver runner: Multi-tenant instance, 8 cpu
func SilverRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) string {
	return Runner(3, daggerVersion, 8, false, dind)
}

// Gold runner: Single-tenant instance, 16 cpu
func GoldRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) string {
	return Runner(3, daggerVersion, 16, true, dind)
}

// Platinum runner: Single-tenant instance, 32 cpu
func PlatinumRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) string {
	return Runner(3, daggerVersion, 32, true, dind)
}
