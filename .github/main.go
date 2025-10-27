package main

import (
	"fmt"
	"strings"

	"github.com/dagger/dagger/.github/internal/dagger"
)

const (
	daggerVersion      = "v0.19.3"
	upstreamRepository = "dagger/dagger"
	ubuntuVersion      = "24.04"
	defaultRunner      = "ubuntu-" + ubuntuVersion
	publicToken        = "dag_dagger_sBIv6DsjNerWvTqt2bSFeigBUqWxp9bhh3ONSSgeFnw" //nolint:gosec
	timeoutMinutes     = 20
)

func module(paths ...string) string {
	path := strings.Join(paths, "/")
	path = strings.TrimPrefix(path, "/")
	if path != "" {
		path = "/" + path
	}
	return "github.com/${{ github.repository }}" + path + "@${{ github.sha }}"
}

type CI struct {
	// Workflows collects all the workflows together, and applies some defaults
	// that can apply across all of our different specific runners
	// +private
	Workflows *dagger.Gha

	GithubRunner       *dagger.Gha // +private
	DaggerRunner       *dagger.Gha // +private
	AltRunner          *dagger.Gha // +private
	AltRunnerWithCache *dagger.Gha // +private
	CloudRunner        *dagger.Gha // +private
}

func New() *CI {
	workflow := dag.Gha().Workflow("", dagger.GhaWorkflowOpts{
		PullRequestConcurrency:      "preempt",
		Permissions:                 []dagger.GhaPermission{dagger.GhaPermissionReadContents},
		OnPushBranches:              []string{"main", "releases/**"},
		OnPullRequestOpened:         true,
		OnPullRequestReopened:       true,
		OnPullRequestSynchronize:    true,
		OnPullRequestReadyForReview: true,
	})

	ci := &CI{
		Workflows: dag.Gha(dagger.GhaOpts{
			JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
				Module:        module(),
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
				Runner:         BronzeRunner(false),
				TimeoutMinutes: timeoutMinutes,
			}),
			WorkflowDefaults: workflow,
		}),
		AltRunner: dag.Gha(dagger.GhaOpts{
			JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
				Runner:         AltGoldRunner(),
				TimeoutMinutes: timeoutMinutes,
			}),
			WorkflowDefaults: workflow,
		}),
		AltRunnerWithCache: dag.Gha(dagger.GhaOpts{
			JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
				Runner:         AltSilverRunnerWithCache(),
				TimeoutMinutes: timeoutMinutes,
			}),
			WorkflowDefaults: workflow,
		}),
		CloudRunner: dag.Gha(dagger.GhaOpts{
			JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
				Runner:         []string{"ubuntu-24.04"},
				TimeoutMinutes: timeoutMinutes,
				CloudEngine:    true,
			}),
			WorkflowDefaults: dag.Gha().Workflow("", dagger.GhaWorkflowOpts{
				PullRequestConcurrency:      "preempt",
				Permissions:                 []dagger.GhaPermission{dagger.GhaPermissionReadContents, dagger.GhaPermissionWriteIdToken},
				OnPushBranches:              []string{"main"},
				OnPullRequestOpened:         true,
				OnPullRequestReopened:       true,
				OnPullRequestSynchronize:    true,
				OnPullRequestReadyForReview: true,
			}),
		}),
	}

	return ci.
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Check generated files",
			"check-generated",
		).
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Lint go packages",
			"check-lint-go",
		).
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Check go tidy",
			"check-tidy",
		).
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Lint SDKs",
			"check-lint-sdks",
		).
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Lint docs",
			"check-lint-docs",
		).
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Lint docs",
			"check-lint-helm",
		).
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Lint docs",
			"check-lint-scripts",
		).
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Release dry run",
			"check-release-dry-run",
		).
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Security scan",
			"check-scan",
		).
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Test Helm chart",
			"check-test-helm",
		).
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Test SDKs",
			"check-test-sdks",
		).
		withWorkflow(
			ci.AltRunnerWithCache,
			false,
			"Test install scripts",
			"check-test-scripts",
		).
		withCoreTestWorkflows(
			ci.AltRunner,
			"Core tests",
		).
		withWorkflow(
			ci.AltRunner,
			true,
			"Dev Engine",
			"check-test-sdks",
		).
		withPrepareReleaseWorkflow().
		withLLMWorkflows()
}

// Generate Github Actions workflows to call our Dagger workflows
func (ci *CI) Generate(
	// +defaultPath="/"
	// +ignore=["*", "!.github"]
	repository *dagger.Directory,
) *dagger.Changeset {
	return ci.Workflows.Generate(dagger.GhaGenerateOpts{
		Directory: repository,
	}).Changes(repository)
}

// Add a workflow with our project-specific defaults
func (ci *CI) withWorkflow(runner *dagger.Gha, devEngine bool, name string, command string) *CI {
	jobOpts := dagger.GhaJobOpts{}
	if devEngine {
		jobOpts.DaggerDev = "${{ github.sha }}"
	}
	w := runner.
		Workflow(name).
		WithJob(runner.Job(name, daggerCommand(command), jobOpts))

	ci.Workflows = ci.Workflows.WithWorkflow(w)
	return ci
}

func (ci *CI) withCoreTestWorkflows(runner *dagger.Gha, name string) *CI {
	w := runner.
		Workflow(name).
		With(splitTests(runner, "test-", false, []testSplit{
			{"cgroupsv2", []string{"TestProvision", "TestTelemetry"}, &dagger.GhaJobOpts{}},
			{"modules", []string{"TestModule"}, &dagger.GhaJobOpts{
				Runner: AltPlatinumRunner(),
			}},
			{"module-runtimes", []string{"TestGo", "TestPython", "TestTypescript", "TestElixir", "TestPHP", "TestJava"}, &dagger.GhaJobOpts{
				Runner: AltPlatinumRunner(),
			}},
			{"container", []string{"TestContainer", "TestDockerfile"}, &dagger.GhaJobOpts{}},
			{"LLM", []string{"TestLLM"}, &dagger.GhaJobOpts{}},
			{"cli-engine", []string{"TestCLI", "TestEngine"}, &dagger.GhaJobOpts{}},
			{"client-generator", []string{"TestClientGenerator"}, &dagger.GhaJobOpts{}},
			{"interface", []string{"TestInterface"}, &dagger.GhaJobOpts{}},
			{"call-and-shell", []string{"TestCall", "TestShell", "TestDaggerCMD"}, &dagger.GhaJobOpts{}},
			{"everything-else", nil, &dagger.GhaJobOpts{
				Runner: AltPlatinumRunner(),
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
			if dev {
				opts.DaggerDev = "${{ github.sha }}"
			}
			w = w.WithJob(runner.Job(name+split.name, command, opts))
		}
		return w
	}
}

func (ci *CI) withPrepareReleaseWorkflow() *CI {
	gha := dag.Gha(dagger.GhaOpts{
		JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
			Runner:         AltBronzeRunnerWithCache(),
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
			"--github-token=env:RELEASE_DAGGER_CI_TOKEN deploy-preview-with-dagger-main --target ${{ github.event.number }} --github-assignee $GITHUB_ACTOR",
			dagger.GhaJobOpts{
				Secrets: []string{"RELEASE_DAGGER_CI_TOKEN"},
				Module:  "modules/daggerverse",
			}))

	ci.Workflows = ci.Workflows.WithWorkflow(w)

	return ci
}

func (ci *CI) withLLMWorkflows() *CI {
	gha := dag.Gha(dagger.GhaOpts{
		JobDefaults: dag.Gha().Job("", "", dagger.GhaJobOpts{
			Runner:         AltBronzeRunnerWithCache(),
			DaggerVersion:  daggerVersion,
			TimeoutMinutes: timeoutMinutes,
		}),
	})
	w := gha.Workflow("llm", dagger.GhaWorkflowOpts{
		// Only run when LLM-related files are changed
		OnPushPaths: []string{
			"core/llm.go",
			"core/mcp.go",
			"core/env.go",
			"core/llm_*.go",
			"core/llm_*.md",
			"core/schema/llm.go",
			"core/schema/env.go",
			"modules/evaluator/**",
			"modules/evals/**",
		},
	}).WithJob(gha.Job(
		"evals",
		"--allow-llm all check",
		dagger.GhaJobOpts{
			Module: module("modules/evals"),
			Runner: AltGoldRunner(),
			// NOTE: avoid running for forks
			Condition: fmt.Sprintf(`${{ (github.repository == '%s') && (github.actor != 'dependabot[bot]') }}`, upstreamRepository),
			Secrets:   []string{"OP_SERVICE_ACCOUNT_TOKEN"},
			Env: []string{
				"ANTHROPIC_API_KEY=op://RelEng/ANTHROPIC/API_KEY",
				"GEMINI_API_KEY=op://RelEng/GEMINI/API_KEY",
				"OPENAI_API_KEY=op://RelEng/OPEN_AI/API_KEY",
			},
		})).WithJob(gha.Job(
		"shell",
		"--allow-llm all test specific --env-file file://.env --pkg ./cmd/dagger --run CMD/LLM",
		dagger.GhaJobOpts{
			Runner: AltGoldRunner(),
			// NOTE: avoid running for forks
			Condition: fmt.Sprintf(`${{ (github.repository == '%s') && (github.actor != 'dependabot[bot]') }}`, upstreamRepository),
			Secrets:   []string{"OP_SERVICE_ACCOUNT_TOKEN"},
			Env: []string{
				"ANTHROPIC_API_KEY=op://RelEng/ANTHROPIC/API_KEY",
				"GEMINI_API_KEY=op://RelEng/GEMINI/API_KEY",
				"OPENAI_API_KEY=op://RelEng/OPEN_AI/API_KEY",
			},
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
) []string {
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
	return []string{fmt.Sprintf(
		"${{ github.repository == '%s' && '%s' || '%s' }}",
		upstreamRepository,
		runner,
		defaultRunner,
	)}
}

// Assemble an alternative 2 runner name for a workflow
func Alt2Runner(
	cpus int,
	cached bool,
) []string {
	mem := cpus * 2
	runner := fmt.Sprintf(
		"nscloud-ubuntu-%s-amd64-%dx%d",
		ubuntuVersion,
		cpus,
		mem)

	// Fall back to default runner if repository is not upstream
	// (this is GHA DSL and will be evaluated by the GHA runner)
	labels := []string{fmt.Sprintf(
		"${{ github.repository == '%s' && '%s' || '%s' }}",
		upstreamRepository,
		runner,
		defaultRunner,
	)}

	if cached {
		dagger := fmt.Sprintf(
			"namespace-experiments:dagger.integration=enabled;dagger.version=%s",
			strings.TrimPrefix(daggerVersion, "v"),
		)
		labels = append(labels, fmt.Sprintf(
			"${{ github.repository == '%s' && '%s' || '%s' }}",
			upstreamRepository,
			dagger,
			defaultRunner,
		))
	}

	return labels
}

// Bronze runner: Multi-tenant instance, 4 cpu
func BronzeRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) []string {
	return Runner(3, daggerVersion, 4, false, dind)
}

// Silver runner: Multi-tenant instance, 8 cpu
func SilverRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) []string {
	return Runner(3, daggerVersion, 8, false, dind)
}

// Gold runner: Single-tenant instance, 16 cpu
func GoldRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) []string {
	return Runner(3, daggerVersion, 16, true, dind)
}

// Platinum runner: Single-tenant instance, 32 cpu
func PlatinumRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) []string {
	return Runner(3, daggerVersion, 32, true, dind)
}

// Alternative Bronze runner with caching: Single-tenant, 4 cpu
func AltBronzeRunnerWithCache() []string {
	return Alt2Runner(4, true)
}

// Alternative Silver runner: Single-tenant with Docker, 8 cpu
func AltSilverRunner() []string {
	return Alt2Runner(8, false)
}

// Alternative Silver runner with caching: Single-tenant, 8 cpu
func AltSilverRunnerWithCache() []string {
	return Alt2Runner(8, true)
}

// Alternative Gold runner: Single-tenant with Docker, 16 cpu
func AltGoldRunner() []string {
	return Alt2Runner(16, false)
}

// Alternative Gold runner with caching: Single-tenant, 16 cpu
func AltGoldRunnerWithCache() []string {
	return Alt2Runner(16, true)
}

// Alternative Platinum runner: Single-tenant with Docker, 32 cpu
func AltPlatinumRunner() []string {
	return Alt2Runner(32, false)
}

// Alternative Platinum runner with caching: Single-tenant, 23 cpu
func AltPlatinumRunnerWithCache() []string {
	return Alt2Runner(32, true)
}
