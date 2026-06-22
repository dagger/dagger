// Package helm contains e2e contract tests for Dagger's Helm chart.
//
//go:test:include dagger.json
//go:test:include k3s-entrypoint.sh
//go:test:include ../../helm/dagger
//go:test:include ../../.changes/.next
//go:test:include ../../analytics
//go:test:include ../../cmd/codegen
//go:test:include ../../cmd/dagger
//go:test:include ../../core/gitref
//go:test:include ../../core/modules
//go:test:include ../../core/openrouter
//go:test:include ../../core/prompts
//go:test:include ../../core/workspace
//go:test:include ../../dagql
//go:test:include ../../engine
//go:test:include ../../go.mod
//go:test:include ../../go.sum
//go:test:include ../../internal
//go:test:include ../../modules/alpine
//go:test:include ../../modules/wolfi
//go:test:include ../../sdk/go
//go:test:include ../../toolchains/cli-dev
//go:test:include ../../toolchains/go
//go:test:include ../../util
//go:test:include ../../version
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
		ok := t.Run(test.name, func(t *testing.T) {
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
		if !ok {
			// All cases deploy the same engine image; if the first one never
			// becomes reachable the rest will hang the same way. Stop early so we
			// fail fast with diagnostics instead of burning the whole package
			// timeout (which previously produced a 10m hang with no output).
			break
		}
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
		return fmt.Errorf("kube-pod query to engine %q failed: %w\n%s", engineName, err, dumpEngineDiagnostics(ctx, kubectl, engineName))
	}

	if port != 0 {
		byTCP := kubectl.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://localhost:4000").
			WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", "v0.16.0-000000000000")
		if err := testDaggerQuery(ctx, fmt.Sprintf("kubectl port-forward --namespace=dagger pods/%s 4000:%d & dagger query", podName, port), byTCP); err != nil {
			return fmt.Errorf("tcp port-forward query to engine %q failed: %w\n%s", engineName, err, dumpEngineDiagnostics(ctx, kubectl, engineName))
		}
	}

	return nil
}

// daggerQueryTimeout bounds how long we wait for the freshly-installed engine to
// answer a query. The dagger client's "connecting to engine" phase retries
// silently with no output, so without this bound a non-serving engine hangs
// until the Go test package timeout (10m) with zero diagnostics. Keep it well
// under that budget so we still have time to collect engine logs on failure.
const daggerQueryTimeout = 4 * time.Minute

func testDaggerQuery(ctx context.Context, command string, kubectl *dagger.Container) error {
	ctx, cancel := context.WithTimeout(ctx, daggerQueryTimeout)
	defer cancel()

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
		return fmt.Errorf("engine did not answer a query within %s (connection never became ready): %w", daggerQueryTimeout, err)
	}
	if !strings.Contains(stdout, "Linux") {
		return fmt.Errorf("expected to be a Linux container, got: %s", stdout)
	}
	return nil
}

// dumpEngineDiagnostics best-effort collects Kubernetes state for the engine pod
// so a failed connection produces actionable output (pod status, engine logs,
// events) instead of an opaque timeout. It never fails the test itself.
func dumpEngineDiagnostics(ctx context.Context, kubectl *dagger.Container, engineName string) string {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	selector := "name=" + engineName
	script := strings.Join([]string{
		"set +e",
		`echo "=== pods (all namespaces) ==="`,
		"kubectl get pods -A -o wide 2>&1",
		`echo "=== describe engine pod ==="`,
		"kubectl describe pod -l " + selector + " -n dagger 2>&1",
		`echo "=== engine logs (current) ==="`,
		"kubectl logs -l " + selector + " -n dagger --all-containers --tail=200 2>&1",
		`echo "=== engine logs (previous, if any) ==="`,
		"kubectl logs -l " + selector + " -n dagger --all-containers --previous --tail=200 2>&1",
		`echo "=== recent events ==="`,
		"kubectl get events -n dagger --sort-by=.lastTimestamp 2>&1",
		"exit 0",
	}, "\n")

	out, err := kubectl.WithExec([]string{"sh", "-c", script}).Stdout(ctx)
	if err != nil {
		return fmt.Sprintf("engine diagnostics for %q (collection error: %v):\n%s", engineName, err, out)
	}
	return fmt.Sprintf("engine diagnostics for %q:\n%s", engineName, out)
}
