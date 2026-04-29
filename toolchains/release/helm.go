package main

import (
	"context"
	"strings"

	"toolchains/release/internal/dagger"

	"helm.sh/helm/v3/pkg/chart"
	"sigs.k8s.io/yaml"
)

// Helm chart versioning and publication live in release now. The former
// helm-dev module only covers chart checks, so release owns the artifact path
// directly instead of depending on a separate toolchain for these steps.
func (r *Release) helmChartSource() *dagger.Directory {
	return dag.CurrentWorkspace().
		Directory("/", dagger.WorkspaceDirectoryOpts{Include: []string{"helm/dagger"}}).
		Directory("helm/dagger")
}

func (r *Release) helmChart() *dagger.Container {
	return dag.Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: []string{
				"helm~3.18.4",
			},
		}).
		WithDirectory("/dagger-helm", r.helmChartSource()).
		WithWorkdir("/dagger-helm")
}

func (r *Release) helmSetVersion(ctx context.Context, version string) (*dagger.File, error) {
	chartYaml, err := r.helmChartSource().File("Chart.yaml").Contents(ctx)
	if err != nil {
		return nil, err
	}

	meta := new(chart.Metadata)
	if err := yaml.Unmarshal([]byte(chartYaml), meta); err != nil {
		return nil, err
	}

	meta.Version = strings.TrimPrefix(version, "v")
	if err := meta.Validate(); err != nil {
		return nil, err
	}

	updatedChart, err := yaml.Marshal(&meta)
	if err != nil {
		return nil, err
	}

	return dag.File("Chart.yaml", string(updatedChart)), nil
}

func (r *Release) helmReleaseDryRun(ctx context.Context) error {
	return r.helmPublish(ctx, "main", nil, true)
}

func (r *Release) helmPublish(ctx context.Context, target string, githubToken *dagger.Secret, dryRun bool) error {
	version := strings.TrimPrefix(target, "helm/chart/")
	_, err := r.helmChart().
		With(func(c *dagger.Container) *dagger.Container {
			if githubToken != nil {
				return c.WithSecretVariable("GITHUB_TOKEN", githubToken)
			}
			return c
		}).
		With(func(c *dagger.Container) *dagger.Container {
			if dryRun {
				return c.WithExec([]string{"helm", "package", "."})
			}
			script := strings.Join([]string{
				"set -x",
				"helm registry login ghcr.io/dagger --username dagger --password $GITHUB_TOKEN",
				"helm package .",
				"helm push dagger-helm-" + strings.TrimPrefix(version, "v") + ".tgz oci://ghcr.io/dagger",
				"helm registry logout ghcr.io/dagger",
			}, " && \\")
			return c.WithExec([]string{"sh", "-c", script})
		}).
		Sync(ctx)
	return err
}
