package main

import (
	"context"
	"strings"

	"github.com/dagger/dagger/dev/internal/util"
)

type Helm struct {
	Dagger *DaggerDev // +private
}

func (h *Helm) Test(ctx context.Context) error {
	helmChart := h.Dagger.Source.Directory("helm/dagger")
	helm := dag.Container().
		From("alpine/helm:3.12.3").
		WithDirectory("/tmp/dagger-helm", helmChart).
		WithWorkdir("/tmp/dagger-helm")

	_, err := helm.
		WithExec([]string{"lint"}).
		WithExec([]string{"lint", "--debug", "--namespace", "dagger", "--set", "magicache.token=hello-world", "--set", "magicache.enabled=true"}).
		WithExec([]string{"template", ".", "--debug", "--namespace", "dagger", "--set", "magicache.token=hello-world", "--set", "magicache.enabled=true"}).
		Sync(ctx)
	return err
}

func (h *Helm) Publish(
	ctx context.Context,
	tag string,

	// +optional
	githubToken *Secret,

	// +optional
	dryRun bool,
) error {
	version := strings.TrimPrefix(tag, "helm/chart/v")

	helmChart := h.Dagger.Source.Directory("helm/dagger")
	helm := dag.Container().
		From("alpine/helm:3.12.3").
		WithDirectory("/tmp/dagger-helm", helmChart).
		WithWorkdir("/tmp/dagger-helm")
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
