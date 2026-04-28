// Package helm contains e2e contract tests for Dagger's Helm chart.
//
// workspace:include k3s-entrypoint.sh
// workspace:include ../../helm/dagger
// workspace:include ../../.changes/.next
// workspace:include ../../analytics
// workspace:include ../../cmd/dagger
// workspace:include ../../core/modules
// workspace:include ../../core/openrouter
// workspace:include ../../dagql
// workspace:include ../../engine
// workspace:include ../../go.mod
// workspace:include ../../go.sum
// workspace:include ../../internal
// workspace:include ../../modules/alpine
// workspace:include ../../modules/wolfi
// workspace:include ../../sdk/go
// workspace:include ../../toolchains/cli-dev
// workspace:include ../../toolchains/go
// workspace:include ../../util
// workspace:include ../../version
package helm

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	dagger "github.com/dagger/dagger/e2e/helm/dagger"
)

const (
	chartPath       = "helm/dagger"
	helmImage       = "cgr.dev/chainguard/wolfi-base"
	testEngineImage = "registry.dagger.io/engine:main"
)

func TestCustomProbes(t *testing.T) {
	ctx := t.Context()
	dag := connect(t)

	customValues := `
engine:
  readinessProbeSettings:
    exec:
      command:
        - sh
        - -c
        - "echo ready"
    initialDelaySeconds: 10
    periodSeconds: 20
  livenessProbeSettings:
    exec:
      command:
        - sh
        - -c
        - "echo alive"
    initialDelaySeconds: 15
    periodSeconds: 30
`

	out, err := helmContainer(dag).
		WithNewFile("/tmp/custom-probes.yaml", customValues).
		WithExec([]string{"helm", "template", ".", "-f", "/tmp/custom-probes.yaml"}).
		Stdout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "echo ready") {
		t.Fatalf("custom readiness probe command not found in rendered template")
	}
	if !strings.Contains(out, "echo alive") {
		t.Fatalf("custom liveness probe command not found in rendered template")
	}
	if strings.Contains(out, "dagger core version") {
		t.Fatalf("default probe command still present after override")
	}
}

func TestPackageDryRun(t *testing.T) {
	ctx := t.Context()
	dag := connect(t)

	_, err := helmContainer(dag).
		WithExec([]string{"helm", "package", "."}).
		Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstallK3S(t *testing.T) {
	ctx := t.Context()
	dag := connect(t)

	k3s := newK3S(dag, "helm-test")
	k3sSvc, err := k3s.service.Start(ctx)
	if err != nil {
		t.Fatalf("start k3s: %v", err)
	}
	t.Cleanup(func() {
		if _, err := k3sSvc.Stop(context.Background()); err != nil {
			t.Logf("stop k3s: %v", err)
		}
	})

	kubectl, err := helmContainer(dag).
		WithMountedFile("/usr/bin/dagger", dag.DaggerCli().Binary()).
		WithServiceBinding("helm-test", k3sSvc).
		WithFile("/.kube/config", k3s.config).
		WithEnvVariable("KUBECONFIG", "/.kube/config").
		WithEnvVariable("CACHEBUSTER", time.Now().String()).
		WithExec([]string{"kubectl", "get", "nodes", "--output=wide"}).
		Sync(ctx)
	if err != nil {
		t.Fatalf("initialize kubectl: %v", err)
	}

	tests := []struct {
		name       string
		release    string
		engineName string
		engineKind string
		port       int
		setValues  []string
	}{
		{
			name:       "default daemonset",
			release:    "dagger",
			engineName: "dagger-dagger-helm-engine",
			engineKind: "DaemonSet",
		},
		{
			name:       "daemonset with port",
			release:    "dagger2",
			engineName: "dagger2-dagger-helm-engine",
			engineKind: "DaemonSet",
			port:       5678,
			setValues:  []string{"engine.port=5678"},
		},
		{
			name:       "statefulset",
			release:    "dagger3",
			engineName: "dagger3-dagger-helm-engine",
			engineKind: "StatefulSet",
			setValues:  []string{"engine.kind=StatefulSet"},
		},
		{
			name:       "statefulset with port configured",
			release:    "dagger4",
			engineName: "dagger4-dagger-helm-engine",
			engineKind: "StatefulSet",
			setValues:  []string{"engine.kind=StatefulSet", "engine.port=5678"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := []string{
				"helm", "install", "--wait", "--create-namespace", "--namespace=dagger",
				"--set=engine.image.ref=" + testEngineImage,
			}
			for _, value := range test.setValues {
				args = append(args, "--set="+value)
			}
			args = append(args, test.release, ".")

			engine, err := kubectl.WithExec(args).Sync(ctx)
			if err != nil {
				t.Fatalf("install chart: %v", err)
			}
			if err := runInstallAssertions(ctx, test.engineName, test.engineKind, test.port, engine); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func connect(t *testing.T) *dagger.Client {
	t.Helper()

	dag, err := dagger.Connect(t.Context())
	if err != nil {
		t.Fatalf("connect to dagger: %v", err)
	}
	t.Cleanup(func() {
		if err := dag.Close(); err != nil {
			t.Errorf("close dagger client: %v", err)
		}
	})
	return dag
}

func helmContainer(dag *dagger.Client) *dagger.Container {
	chart := dag.CurrentWorkspace().
		Directory("/", dagger.WorkspaceDirectoryOpts{Include: []string{chartPath}}).
		Directory(chartPath)

	return dag.Container().
		From(helmImage).
		WithExec([]string{"apk", "add", "--no-cache", "helm~3.18.4", "kubectl"}).
		WithDirectory("/dagger-helm", chart).
		WithWorkdir("/dagger-helm")
}

func runInstallAssertions(ctx context.Context, engineName string, engineKind string, port int, kubectl *dagger.Container) error {
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
	}).Stdout(ctx)
	if err != nil {
		return err
	}
	if !strings.Contains(stdout, "Linux") {
		return fmt.Errorf("expected to be a Linux container, got: %s", stdout)
	}
	return nil
}
