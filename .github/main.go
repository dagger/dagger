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

var (
	baseJobOpts = dagger.GhaPipelineWithJobOpts{
		PublicToken:    publicToken,
		DaggerVersion:  daggerVersion,
		Runner:         []string{BronzeRunner(false)},
		TimeoutMinutes: timeoutMinutes,
	}
	basePipelineOpts = dagger.GhaPipelineOpts{
		OnPushBranches:              []string{"main"},
		OnPullRequestOpened:         true,
		OnPullRequestReopened:       true,
		OnPullRequestSynchronize:    true,
		OnPullRequestReadyForReview: true,
		PullRequestConcurrency:      "preempt",
		Permissions:                 []dagger.GhaPermission{dagger.ReadContents},
	}
)

type CI struct {
	// +private
	Pipelines []*dagger.GhaPipeline
}

func New() *CI {
	ci := &CI{
		Pipelines: []*dagger.GhaPipeline{},
	}

	return ci.
		WithPipeline(
			"Docs",
			"docs lint",
			nil,
			false,
		).
		WithSdkPipelines(
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

// Add a pipeline with our project-specific defaults
func (ci *CI) WithPipeline(
	// Pipeline name
	name string,
	// Pipeline command
	command string,
	// +optional
	runner []string,
	// Build the local engine source, and run the pipeline with it
	// +optional
	devEngine bool,
) *CI {
	jobOpts := baseJobOpts
	if devEngine {
		jobOpts.DaggerVersion = "."
	}
	if len(runner) != 0 {
		jobOpts.Runner = runner
	}

	ci.Pipelines = append(ci.Pipelines,
		dag.Gha().
			Pipeline(name, basePipelineOpts).
			WithJob(name, daggerCommand(command), jobOpts))

	return ci
}

func (ci *CI) WithSdkPipelines(name string, sdks ...string) *CI {
	p := dag.Gha().Pipeline(name, basePipelineOpts)

	for _, sdk := range sdks {
		command := daggerCommand("check --targets=sdk/" + sdk)
		devJobOpts := baseJobOpts
		devJobOpts.DaggerVersion = "."
		devJobOpts.Runner = []string{SilverRunner(true)}

		p = p.
			WithJob(sdk, command, baseJobOpts).
			WithJob(sdk+"-dev", command, devJobOpts)
	}

	ci.Pipelines = append(ci.Pipelines, p)

	return ci
}

func daggerCommand(command string) string {
	return fmt.Sprintf(`--ref="$GITHUB_REF" --docker-cfg=file:$HOME/.docker/config.json %s`, command)
}

// Assemble a runner name for a pipeline
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

// Generate Github Actions pipelines to call our Dagger pipelines
func (ci *CI) Generate() *dagger.Directory {
	return dag.Gha().Generate(ci.Pipelines)
}

func (ci *CI) Check(ctx context.Context,
	// +defaultPath="/"
	// +ignore=["!.github"]
	repository *dagger.Directory,
) error {
	return dag.Dirdiff().AssertEqual(ctx, repository, ci.Generate(), []string{".github/workflows"})
}
