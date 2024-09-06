package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/helm/internal/dagger"

	"github.com/moby/buildkit/identity"
	"helm.sh/helm/v3/pkg/chart"
	"sigs.k8s.io/yaml"
)

func New(
	// The dagger helm chart directory
	// +optional
	// +defaultPath="/helm/dagger"
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
		WithExec([]string{"helm", "install", "--wait", "--create-namespace", "--namespace=dagger", "dagger", "."}).
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

	// Version to set the chart & app to, e.g. --version=v0.12.0
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
	meta.AppVersion = version

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

// Package & publish chart to our registry
func (h *Helm) Publish(
	ctx context.Context,
	tag string,
	// +optional
	githubToken *dagger.Secret,
	// +optional
	dryRun bool,
) error {
	version := strings.TrimPrefix(tag, "helm/chart/v")
	helm := h.chart()

	if githubToken != nil {
		helm = helm.WithSecretVariable("GITHUB_TOKEN", githubToken)
	}
	var script string
	if dryRun {
		script = "helm package ."
	} else {
		script = strings.Join([]string{
			"helm registry login ghcr.io/dagger --username dagger --password $GITHUB_TOKEN",
			"helm package .",
			"helm push dagger-helm-" + version + ".tgz oci://ghcr.io/dagger",
			"helm registry logout ghcr.io/dagger",
		}, " && \\")
	}
	_, err := helm.
		WithExec([]string{"sh", "-c", script}).
		Sync(ctx)
	return err
}
