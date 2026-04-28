// Lint the Dagger Helm chart.
package main

import (
	"context"

	"dagger/helm/internal/dagger"
)

const helmImage = "cgr.dev/chainguard/wolfi-base"

type HelmDev struct {
	Chart *dagger.Directory // +private
}

func New(
	// The Dagger Helm chart directory.
	// +optional
	// +defaultPath="/helm/dagger"
	chart *dagger.Directory,
) *HelmDev {
	return &HelmDev{Chart: chart}
}

// Lint the Dagger Helm chart.
// +check
func (h *HelmDev) Lint(ctx context.Context) error {
	_, err := h.chart().
		WithExec([]string{"helm", "lint"}).
		WithExec([]string{"helm", "lint", "--debug", "--namespace=dagger", "--set=magicache.token=hello-world", "--set=magicache.enabled=true"}).
		WithExec([]string{"helm", "template", ".", "--debug", "--namespace=dagger", "--set=magicache.token=hello-world", "--set=magicache.enabled=true"}).
		Sync(ctx)
	return err
}

func (h *HelmDev) chart() *dagger.Container {
	return dag.Container().
		From(helmImage).
		WithExec([]string{"apk", "add", "--no-cache", "helm~3.18.4"}).
		WithDirectory("/dagger-helm", h.Chart).
		WithWorkdir("/dagger-helm")
}
