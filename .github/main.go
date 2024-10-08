package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/.github/internal/dagger"
)

const (
	daggerVersion      = "v0.13.3"
	upstreamRepository = "dagger/dagger"
	defaultRunner      = "ubuntu-latest"
)

type CI struct {
	// +private
	Gha *dagger.Gha
}

func New(
	// The dagger repository
	// +optional
	// +defaultPath="/"
	// +ignore=["!.github"]
	repository *dagger.Directory,
) *CI {
	ci := new(CI)
	ci.Gha = dag.Gha(dagger.GhaOpts{
		DaggerVersion: daggerVersion,
		PublicToken:   "dag_dagger_sBIv6DsjNerWvTqt2bSFeigBUqWxp9bhh3ONSSgeFnw",
		Runner:        ci.BronzeRunner(false),
		Repository:    repository,
	})
	return ci.
		WithPipeline("Docs", "docs lint", "", false).
		WithSdkPipelines("python").
		WithSdkPipelines("typescript").
		WithSdkPipelines("go").
		WithSdkPipelines("java").
		WithSdkPipelines("elixir").
		WithSdkPipelines("rust").
		WithSdkPipelines("php")
}

// Add a pipeline with our project-specific defaults
func (ci *CI) WithPipeline(
	// Pipeline name
	name string,
	// Pipeline command
	command string,
	// +optional
	runner string,
	// Build the local engine source, and run the pipeline with it
	// +optional
	devEngine bool,
) *CI {
	opts := dagger.GhaWithPipelineOpts{
		OnPushBranches:              []string{"main"},
		OnPullRequestOpened:         true,
		OnPullRequestReopened:       true,
		OnPullRequestSynchronize:    true,
		OnPullRequestReadyForReview: true,
		PullRequestConcurrency:      "preempt",
		TimeoutMinutes:              10,
		Permissions:                 []dagger.GhaPermission{dagger.ReadContents},
	}
	if runner != "" {
		opts.Runner = runner
	}
	if devEngine {
		opts.DaggerVersion = "."
	} else {
		opts.DaggerVersion = daggerVersion
	}
	command = fmt.Sprintf("--ref=\"$GITHUB_REF\" --docker-cfg=file:$HOME/.docker/config.json %s", command)
	ci.Gha = ci.Gha.WithPipeline(name, command, opts)
	return ci
}

func (ci *CI) WithSdkPipelines(sdk string) *CI {
	return ci.
		WithPipeline(
			sdk,
			"check --targets=sdk/"+sdk,
			"",
			false,
		).
		WithPipeline(
			sdk+"-dev",
			"check --targets=sdk/"+sdk,
			ci.SilverRunner(true),
			true,
		)
}

// Assemble a runner name for a pipeline
func (ci *CI) Runner(
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
	// We only want on-demand instances, spot ones are too disruptive
	runner += "-od"

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
func (ci *CI) BronzeRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) string {
	return ci.Runner(2, daggerVersion, 4, false, dind)
}

// Silver runner: Multi-tenant instance, 8 cpu
func (ci *CI) SilverRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) string {
	return ci.Runner(2, daggerVersion, 8, false, dind)
}

// Gold runner: Single-tenant instance, 16 cpu
func (ci *CI) GoldRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) string {
	return ci.Runner(2, daggerVersion, 16, true, dind)
}

// Platinum runner: Single-tenant instance, 32 cpu
func (ci *CI) PlatinumRunner(
	// Enable docker-in-docker
	// +optional
	dind bool,
) string {
	return ci.Runner(2, daggerVersion, 32, true, dind)
}

// Generate Github Actions pipelines to call our Dagger pipelines
func (ci *CI) Generate() *dagger.Directory {
	return ci.Gha.Config()
}

func (ci *CI) Check(ctx context.Context) error {
	return dag.Dirdiff().AssertEqual(ctx, ci.Gha.Settings().Repository(), ci.Generate(), []string{".github/workflows"})
}
