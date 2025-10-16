package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"dagger/helm/internal/dagger"

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
func (h *Helm) Lint(ctx context.Context) (MyCheckStatus, error) {
	_, err := h.chart().
		WithExec([]string{"helm", "lint"}).
		WithExec([]string{"helm", "lint", "--debug", "--namespace=dagger", "--set=magicache.token=hello-world", "--set=magicache.enabled=true"}).
		WithExec([]string{"helm", "template", ".", "--debug", "--namespace=dagger", "--set=magicache.token=hello-world", "--set=magicache.enabled=true"}).
		Sync(ctx)

	return CheckCompleted, err
}

// Test the helm chart on an ephemeral K3S service
func (h *Helm) Test(ctx context.Context) (MyCheckStatus, error) {
	k3s := dag.K3S("helm-test")
	// NOTE: force starting here - without this, the config won't be generated
	k3ssvc, err := k3s.Server().Start(ctx)
	if err != nil {
		return CheckCompleted, err
	}
	kubectl, err := h.chart().
		WithMountedFile("/usr/bin/dagger", dag.DaggerCli().Binary()).
		WithServiceBinding("helm-test", k3ssvc).
		WithFile("/.kube/config", k3s.Config()).
		WithEnvVariable("KUBECONFIG", "/.kube/config").
		WithEnvVariable("CACHEBUSTER", rand.Text()).
		WithExec([]string{"kubectl", "get", "nodes", "--output=wide"}).
		Sync(ctx)
	if err != nil {
		return CheckCompleted, err
	}

	engine, err := kubectl.
		WithExec([]string{
			"helm", "install", "--wait", "--create-namespace", "--namespace=dagger",
			"--set=engine.image.ref=registry.dagger.io/engine:main",
			"dagger", ".",
		}).
		Sync(ctx)
	if err != nil {
		return CheckCompleted, err
	}
	err = runTests(ctx, "dagger-dagger-helm-engine", "DaemonSet", 0, engine)
	if err != nil {
		return CheckCompleted, err
	}

	engineWithPort, err := kubectl.
		WithExec([]string{
			"helm", "install", "--wait", "--create-namespace", "--namespace=dagger",
			"--set=engine.image.ref=registry.dagger.io/engine:main",
			"--set=engine.port=5678",
			"dagger2", ".",
		}).
		Sync(ctx)
	if err != nil {
		return CheckCompleted, err
	}
	err = runTests(ctx, "dagger2-dagger-helm-engine", "DaemonSet", 5678, engineWithPort)
	if err != nil {
		return CheckCompleted, err
	}

	engineStateful, err := kubectl.
		WithExec([]string{
			"helm", "install", "--wait", "--create-namespace", "--namespace=dagger",
			"--set=engine.image.ref=registry.dagger.io/engine:main",
			"--set=engine.kind=StatefulSet",
			"dagger3", ".",
		}).
		Sync(ctx)
	if err != nil {
		return CheckCompleted, err
	}
	err = runTests(ctx, "dagger3-dagger-helm-engine", "StatefulSet", 0, engineStateful)
	if err != nil {
		return CheckCompleted, err
	}

	engineStatefulWithPort, err := kubectl.
		WithExec([]string{
			"helm", "install", "--wait", "--create-namespace", "--namespace=dagger",
			"--set=engine.image.ref=registry.dagger.io/engine:main",
			"--set=engine.kind=StatefulSet",
			"--set=engine.port=5678",
			"dagger4", ".",
		}).
		Sync(ctx)
	if err != nil {
		return CheckCompleted, err
	}
	err = runTests(ctx, "dagger4-dagger-helm-engine", "StatefulSet", 0, engineStatefulWithPort)
	if err != nil {
		return CheckCompleted, err
	}

	return CheckCompleted, nil
}

func (h *Helm) chart() *dagger.Container {
	return dag.Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: []string{
				"helm~3.18.4",
				"kubectl",
			},
		}).
		WithDirectory("/dagger-helm", h.Chart).
		WithWorkdir("/dagger-helm")
}

func runTests(ctx context.Context, engineName string, engineKind string, port int, kubectl *dagger.Container) error {
	podName, err := kubectl.WithExec([]string{
		"kubectl", "get", "pod",
		"--selector=name=" + engineName,
		"--namespace=dagger",
		"--output=jsonpath={.items[0].metadata.name}",
	}).Stdout(ctx)
	if err != nil {
		return err
	}

	kind, err := kubectl.WithExec([]string{
		"kubectl", "get", "pod",
		"--selector=name=" + engineName,
		"--namespace=dagger",
		"--output=jsonpath={.items[0].metadata.ownerReferences[0].kind}",
	}).Stdout(ctx)
	if err != nil {
		return err
	}
	if !strings.Contains(kind, engineKind) {
		return fmt.Errorf("expected to be a %s, got: %s", engineKind, kind)
	}

	byKubePod := kubectl.
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", fmt.Sprintf("kube-pod://%s?namespace=dagger", podName)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", "v0.16.0-000000000000")
	if err := testDaggerQuery(ctx, "dagger query", byKubePod); err != nil {
		return err
	}

	if port != 0 {
		byTCP := kubectl.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://localhost:4000").
			WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", "v0.16.0-000000000000")
		if err := testDaggerQuery(ctx, fmt.Sprintf("kubectl port-forward --namespace=dagger pods/%s 4000:%d & dagger query", podName, port), byTCP); err != nil {
			return err
		}
	}

	return nil
}

func testDaggerQuery(ctx context.Context, command string, kubectl *dagger.Container) error {
	stdout, err := kubectl.WithExec([]string{"sh", "-c", command}, dagger.ContainerWithExecOpts{
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
		return fmt.Errorf("expected to be a Linux container, got: %s", stdout)
	}
	return nil
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

func (h *Helm) ReleaseDryRun(ctx context.Context) (MyCheckStatus, error) {
	return CheckCompleted, h.Publish(ctx,
		"main", // target
		nil,    // githubToken
		true,   // dryRun
	)
}

// Package & publish chart to our registry + github release
// +cache="session"
func (h *Helm) Publish(
	ctx context.Context,
	// The git ref to publish
	// eg. "helm/chart/v0.13.0"
	target string,

	// +optional
	githubToken *dagger.Secret,

	// Test as much as possible without actually publishing anything
	// +optional
	dryRun bool,
) error {
	version := strings.TrimPrefix(target, "helm/chart/")
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
