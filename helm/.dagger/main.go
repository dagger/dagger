package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/helm/internal/dagger"

	"github.com/moby/buildkit/identity"
	"golang.org/x/mod/semver"
	"helm.sh/helm/v3/pkg/chart"
	"sigs.k8s.io/yaml"
)

func New(
	// The dagger helm chart directory
	// +optional
	// +defaultPath="./dagger"
	chart *dagger.Directory,
) *Helm {
	return &Helm{
		Chart: chart,
	}
}

type Helm struct {
	Chart *dagger.Directory // +private
}

// Lint the helm chart
func (h *Helm) Lint(ctx context.Context) error {
	_, err := h.chart().
		WithExec([]string{"helm", "lint"}).
		WithExec([]string{"helm", "lint", "--debug", "--namespace=dagger", "--set=magicache.token=hello-world", "--set=magicache.enabled=true"}).
		WithExec([]string{"helm", "template", ".", "--debug", "--namespace=dagger", "--set=magicache.token=hello-world", "--set=magicache.enabled=true"}).
		Sync(ctx)

	return err
}

// Test the helm chart on an ephemeral K3S service
func (h *Helm) Test(ctx context.Context) error {
	k3s := dag.K3S("helm-test")
	// NOTE: force starting here - without this, the config won't be generated
	k3ssvc, err := k3s.Server().Start(ctx)
	if err != nil {
		return err
	}
	test, err := h.chart().
		WithMountedFile("/usr/bin/dagger", dag.DaggerCli().Binary()).
		WithServiceBinding("helm-test", k3ssvc).
		WithFile("/.kube/config", k3s.Config()).
		WithEnvVariable("KUBECONFIG", "/.kube/config").
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithExec([]string{"kubectl", "get", "nodes"}).
		WithExec([]string{"helm", "install", "--wait", "--create-namespace", "--namespace=dagger", "--set=engine.image.ref=registry.dagger.io/engine:main", "dagger", "."}).
		Sync(ctx)
	if err != nil {
		return err
	}
	podName, err := test.
		WithExec([]string{
			"kubectl", "get", "pod",
			"--selector=name=dagger-dagger-helm-engine",
			"--namespace=dagger",
			"--output=jsonpath={.items[0].metadata.name}",
		}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	stdout, err := test.
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", fmt.Sprintf("kube-pod://%s?namespace=dagger", podName)).
		WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{
			Stdin: `{
				container {
					from(address:"alpine") {
						withExec(args: ["uname", "-a"]) { stdout }
					}
				}
			}`,
		}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	if !strings.Contains(stdout, "Linux") {
		return fmt.Errorf("container didn't seem to be running linux")
	}
	return nil
}

func (h *Helm) chart() *dagger.Container {
	return dag.Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: []string{
				"helm",
				"kubectl",
			},
		}).
		WithDirectory("/dagger-helm", h.Chart).
		WithWorkdir("/dagger-helm")
}

// Set chart & app version
func (h *Helm) SetVersion(
	ctx context.Context,
	// Version to set the chart to, e.g. --version=v0.12.0
	version string,
) (*dagger.File, error) {
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

	version = strings.TrimPrefix(version, "v")
	meta.Version = version

	err = meta.Validate()
	if err != nil {
		return nil, err
	}

	updatedChart, err := yaml.Marshal(&meta)
	if err != nil {
		return nil, err
	}

	updatedChartYaml := c.
		WithNewFile("Chart.yaml", string(updatedChart)).
		File("Chart.yaml")

	return updatedChartYaml, nil
}

// Package & publish chart to our registry + github release
func (h *Helm) Publish(
	ctx context.Context,
	// The git ref to publish
	// eg. "helm/dagger/v0.13.0"
	target string,
	// +optional
	// +default="https://github.com/dagger/dagger.git"
	gitRepoSource string,

	// +optional
	githubToken *dagger.Secret,
	// +optional
	discordWebhook *dagger.Secret,

	// Test as much as possible without actually publishing anything
	// +optional
	dryRun bool,
) error {
	version := strings.TrimPrefix(target, "helm/chart/")
	// 1. Package and publish on registry
	_, err := h.chart().
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
				"helm registry login ghcr.io/dagger --username dagger --password $GITHUB_TOKEN",
				"helm package .",
				"helm push dagger-helm-" + strings.TrimPrefix(version, "v") + ".tgz oci://ghcr.io/dagger",
				"helm registry logout ghcr.io/dagger",
			}, " && \\")
			return c.WithExec([]string{"sh", "-c", script})
		}).
		Sync(ctx)
	if err != nil {
		return err
	}
	// 2. Publish on github release
	if semver.IsValid(version) {
		if err := dag.Releaser().GithubRelease(ctx, gitRepoSource, "helm/chart/"+version, target, dagger.ReleaserGithubReleaseOpts{
			Notes:  dag.Releaser().ChangeNotes("helm/dagger", version),
			Token:  githubToken,
			DryRun: dryRun,
		}); err != nil {
			return err
		}

		if err := dag.Releaser().Notify(ctx, gitRepoSource, "helm/chart/"+version, "☸️ Helm Chart", dagger.ReleaserNotifyOpts{
			DiscordWebhook: discordWebhook,
			DryRun:         dryRun,
		}); err != nil {
			return err
		}
	}
	return nil
}
