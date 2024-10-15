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
	daggerCloudToken   = "dag_dagger_sBIv6DsjNerWvTqt2bSFeigBUqWxp9bhh3ONSSgeFnw"
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
		PublicToken:   daggerCloudToken,
		Repository:    repository,
	})

	return ci
}

// Configure all pipelines with Dagger Runners
func (ci *CI) DaggerRunners() *CI {
	silverRunner := []string{ci.SilverRunner(true)}
	return ci.
		WithPipeline("Docs", "docs lint", nil, false).
		WithPipeline("python", "check --targets=sdk/python", nil, false).
		WithPipeline("python-dev", "check --targets=sdk/python", silverRunner, true).
		WithPipeline("typescript", "check --targets=sdk/typescript", nil, false).
		WithPipeline("typescript-dev", "check --targets=sdk/typescript", silverRunner, true).
		WithPipeline("go", "check --targets=sdk/go", nil, false).
		WithPipeline("go-dev", "check --targets=sdk/go", silverRunner, true).
		WithPipeline("java", "check --targets=sdk/java", nil, false).
		WithPipeline("java-dev", "check --targets=sdk/java", silverRunner, true).
		WithPipeline("elixir", "check --targets=sdk/elixir", nil, false).
		WithPipeline("elixir-dev", "check --targets=sdk/elixir", silverRunner, true).
		WithPipeline("rust", "check --targets=sdk/rust", nil, false).
		WithPipeline("rust-dev", "check --targets=sdk/rust", silverRunner, true).
		WithPipeline("php", "check --targets=sdk/php", nil, false).
		WithPipeline("php-dev", "check --targets=sdk/php", silverRunner, true)
}

// Configure all pipelines with Namespace Runners
func (ci *CI) NamespaceRunners() *CI {
	namespaceRunner := []string{"nscloud-ubuntu-24.04-amd64-4x8"}
	return ci.
		WithPipeline("Docs", "docs lint", namespaceRunner, false).
		WithPipeline("python", "check --targets=sdk/python", namespaceRunner, false).
		WithPipeline("python-dev", "check --targets=sdk/python", namespaceRunner, true).
		WithPipeline("typescript", "check --targets=sdk/typescript", namespaceRunner, false).
		WithPipeline("typescript-dev", "check --targets=sdk/typescript", namespaceRunner, true).
		WithPipeline("go", "check --targets=sdk/go", namespaceRunner, false).
		WithPipeline("go-dev", "check --targets=sdk/go", namespaceRunner, true).
		WithPipeline("java", "check --targets=sdk/java", namespaceRunner, false).
		WithPipeline("java-dev", "check --targets=sdk/java", namespaceRunner, true).
		WithPipeline("elixir", "check --targets=sdk/elixir", namespaceRunner, false).
		WithPipeline("elixir-dev", "check --targets=sdk/elixir", namespaceRunner, true).
		WithPipeline("rust", "check --targets=sdk/rust", namespaceRunner, false).
		WithPipeline("rust-dev", "check --targets=sdk/rust", namespaceRunner, true).
		WithPipeline("php", "check --targets=sdk/php", namespaceRunner, false).
		WithPipeline("php-dev", "check --targets=sdk/php", namespaceRunner, true)
}

// Add a pipeline with our project-specific defaults
func (ci *CI) WithPipeline(
	// Pipeline name
	name string,
	// Pipeline command
	command string,
	// Runner to use
	// +optional
	runner []string,
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
	if runner == nil {
		opts.Runner = []string{ci.BronzeRunner(false)}
	} else {
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
