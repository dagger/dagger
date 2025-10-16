package main

import (
	"fmt"
	"strings"

	"github.com/dagger/dagger/.github/internal/dagger"
)

const (
	daggerVersion      = "v0.19.4"
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

	AltRunner          *dagger.Gha // +private
	AltRunnerWithCache *dagger.Gha // +private
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
	}
	return ci.
		withSimpleDaggerCheck("check-generated", "Check generated files").
		withSimpleDaggerCheck("go lint", "Run Go Linter").
		withSimpleDaggerCheck("lint-sdks", "Run all SDK linters").
		withSimpleDaggerCheck("lint-misc", "Lint docs, helm chart and install scripts").
		withSimpleDaggerCheck("go check-tidy", "Check go tidy").
		withSimpleDaggerCheck("release-dry-run", "Release dry run").
		withSimpleDaggerCheck("scan", "Security scan").
		withSimpleDaggerCheck("test-helm", "Test Helm chart").
		withSimpleDaggerCheck("test-sdks", "Test SDKs").
		withSimpleDaggerCheck("scripts test", "Test install scripts").
		withSimpleDaggerCheck("ci-in-ci", "CI in CI").
		withCoreTestWorkflows(ci.AltRunner, "Core tests").
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

// Add a simple workflow that runs 'dagger call' in a standard template
func (ci *CI) withSimpleDaggerCheck(command, name string) *CI {
	template := ci.AltRunnerWithCache
	ci.Workflows = ci.Workflows.WithWorkflow(
		template.
			Workflow(name).
			WithJob(template.Job(name, daggerCommand(command))),
	)
	return ci
}

func (ci *CI) withCoreTestWorkflows(runner *dagger.Gha, name string) *CI {
	w := runner.
		Workflow(name).
		With(splitTests(runner, "test-", []testSplit{
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
func splitTests(runner *dagger.Gha, name string, splits []testSplit) dagger.WithGhaWorkflowFunc {
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
// Namespace runner with optional namespace-managed engine
func Alt2Runner(
	cpus int,
	// If true: namespace will provision a managed dagger engine, and configure
	// the CI runner to connect to that engine ("depot-style" architecture)
	// As of 2025-oct-23, this is the default workhorse runner
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

// Alternative Bronze runner with caching: Single-tenant, 4 cpu
func AltBronzeRunnerWithCache() []string {
	return Alt2Runner(4, true)
}

// Alternative Silver runner with caching: Single-tenant, 8 cpu
func AltSilverRunnerWithCache() []string {
	return Alt2Runner(8, true)
}

// Alternative Gold runner: Single-tenant with Docker, 16 cpu
func AltGoldRunner() []string {
	return Alt2Runner(16, false)
}

// Alternative Platinum runner: Single-tenant with Docker, 32 cpu
func AltPlatinumRunner() []string {
	return Alt2Runner(32, false)
}
