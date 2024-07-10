package main

import (
	"context"
	"strings"

	"github.com/dagger/dagger/dev/internal/util"
	"helm.sh/helm/v3/pkg/chart"
	"sigs.k8s.io/yaml"
)

const (
	// https://hub.docker.com/r/alpine/helm/tags
	// Pin image to ref so that it caches better & protects from tag overrides
	helmImage = "alpine/helm:3.15.2@sha256:761b0f39033ade8ce9e52e03d9d1608d6ca9cad1c7e68dc3e005f9e4e244410e"
)

type Helm struct {
	Source *Directory // +private
}

func (h *Helm) Test(ctx context.Context) error {
	_, err := h.chart().
		WithExec([]string{"lint"}).
		WithExec([]string{"lint", "--debug", "--namespace", "dagger", "--set", "magicache.token=hello-world", "--set", "magicache.enabled=true"}).
		WithExec([]string{"template", ".", "--debug", "--namespace", "dagger", "--set", "magicache.token=hello-world", "--set", "magicache.enabled=true"}).
		Sync(ctx)

	return err
}

func (h *Helm) chart() *Container {
	return dag.Container().
		From(helmImage).
		WithDirectory("/dagger-helm", h.Source).
		WithWorkdir("/dagger-helm")
}

// Set chart & app version
func (h *Helm) SetVersion(
	ctx context.Context,

	// Version to set the chart & app to, e.g. --version=0.12.0
	version string,
) (*File, error) {
	c := h.chart()
	chartYaml, err := c.File("Chart.yaml").Contents(ctx)
	if err != nil {
		return nil, err
	}
	meta := new(chart.Metadata)
	err = yaml.Unmarshal([]byte(chartYaml), meta)
	if err != nil {
		return nil, err
	}

	meta.Version = version
	meta.AppVersion = version

	err = meta.Validate()
	if err != nil {
		return nil, err
	}

	updatedChart, err := yaml.Marshal(&meta)
	if err != nil {
		return nil, err
	}

	updatedChartYaml := c.WithNewFile("Chart.yaml", ContainerWithNewFileOpts{
		Contents: string(updatedChart),
	}).File("Chart.yaml")

	return updatedChartYaml, nil
}

// Package & publish chart to our registry
func (h *Helm) Publish(
	ctx context.Context,
	tag string,

	// +optional
	githubToken *Secret,

	// +optional
	dryRun bool,
) error {
	version := strings.TrimPrefix(tag, "helm/chart/v")
	helm := h.chart()

	if githubToken != nil {
		helm = helm.WithSecretVariable("GITHUB_TOKEN", githubToken)
	}

	pkgCmd := "helm package ."

	if dryRun {
		helm = helm.With(util.ShellCmd(pkgCmd))
	} else {
		helm = helm.
			With(util.ShellCmds(
				"helm registry login ghcr.io/dagger --username dagger --password $GITHUB_TOKEN",
				pkgCmd,
				"helm push dagger-helm-"+version+".tgz oci://ghcr.io/dagger",
				"helm registry logout ghcr.io/dagger",
			))
	}

	_, err := helm.Sync(ctx)
	return err
}
