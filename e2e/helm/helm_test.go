// Package helm contains e2e contract tests for Dagger's Helm chart.
//
//go:test:include dagger.json
//go:test:include k3s-entrypoint.sh
//go:test:include ../../.git
//go:test:include ../../helm/dagger
//go:test:include ../../analytics
//go:test:include ../../cmd/codegen
//go:test:include ../../cmd/dagger
//go:test:include ../../core/artifact
//go:test:include ../../core/dagaddress
//go:test:include ../../core/gitref
//go:test:include ../../core/modules
//go:test:include ../../core/modelcatalog
//go:test:include ../../core/prompts
//go:test:include ../../core/sdk/sdkmeta
//go:test:include ../../core/workspace
//go:test:include ../../dagql
//go:test:include ../../engine
//go:test:include ../../go.mod
//go:test:include ../../go.sum
//go:test:include ../../internal
//go:test:include ../../sdk/go
//go:test:include ../../.dagger/modules/cli-dev
//go:test:include ../../.dagger/modules/go
//go:test:include ../../util
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

	// diagnosticsReserve is how long before go test's deadline TestInstallK3S
	// stops waiting, so it can log diagnostics instead of being killed.
	diagnosticsReserve = time.Minute
	diagnosticsTimeout = 45 * time.Second
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
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-diagnosticsReserve))
		defer cancel()
	}
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

	// kube reaches the cluster without the Dagger CLI, so diagnostics don't
	// wait for the CLI build.
	kube := helmContainer(dag).
		WithServiceBinding("helm-test", k3sSvc).
		WithFile("/.kube/config", k3s.config).
		WithEnvVariable("KUBECONFIG", "/.kube/config").
		WithEnvVariable("CACHEBUSTER", time.Now().String())
	kubectl, err := kube.
		WithMountedFile("/usr/bin/dagger", dag.DaggerCli().Binary()).
		// Kubeconfig and the k3s service port can be available before API discovery is ready.
		WithExec([]string{"sh", "-c", `
set -eu

for i in $(seq 1 90); do
	if kubectl --request-timeout=5s get nodes --output=wide &&
		kubectl wait --for=condition=Ready nodes --all --timeout=5s; then
		exit 0
	fi
	sleep 1
done

kubectl get nodes --output=wide || true
kubectl get pods -A || true
exit 1
`}).
		Sync(ctx)
	if err != nil {
		logDiagnostics(t, k3s, kube, "")
		t.Fatalf("wait for k3s readiness: %v", err)
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
		if ctx.Err() != nil {
			t.Errorf("skipping %q and later subtests: %v", test.name, context.Cause(ctx))
			break
		}
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
				logDiagnostics(t, k3s, kube, test.engineName)
				t.Fatalf("install chart: %v", err)
			}
			if err := runInstallAssertions(ctx, test.engineName, test.engineKind, test.port, engine); err != nil {
				logDiagnostics(t, k3s, kube, test.engineName)
				t.Fatal(err)
			}
		})
	}
}

// clusterDiagnostics prints a bounded snapshot of the cluster. $ENGINE names
// the engine pods to describe and read logs from, if any.
const clusterDiagnostics = `
run() {
	lines=$1
	shift
	echo "\$ $*"
	"$@" --request-timeout=10s 2>&1 | tail -n "$lines"
}
run 20 kubectl get nodes --output=wide
run 30 kubectl get pods --all-namespaces --output=wide
run 30 kubectl get events --all-namespaces --sort-by=.lastTimestamp
if [ -n "$ENGINE" ]; then
	run 60 kubectl describe pods --namespace=dagger --selector=name="$ENGINE"
	run 30 kubectl logs --namespace=dagger --selector=name="$ENGINE" --all-containers --tail=30
fi
`

// logDiagnostics logs why the cluster may be stuck: the end of the k3s server
// log and a snapshot of the cluster. It has its own time budget, since the
// test's context may already have expired.
func logDiagnostics(t *testing.T, k3s k3sCluster, kube *dagger.Container, engineName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), diagnosticsTimeout)
	defer cancel()

	serverLog, err := k3s.serverLogTail(ctx)
	if err != nil {
		serverLog = fmt.Sprintf("read k3s server log: %v", err)
	}
	t.Logf("k3s server log, last warnings and errors:\n%s", serverLog)

	cluster, err := kube.
		WithEnvVariable("ENGINE", engineName).
		WithEnvVariable("DIAGNOSTICS", time.Now().String()).
		WithExec([]string{"sh", "-c", clusterDiagnostics}).
		Stdout(ctx)
	if err != nil {
		cluster = fmt.Sprintf("get cluster state: %v", err)
	}
	t.Logf("cluster state:\n%s", cluster)
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
		WithExec([]string{"apk", "add", "--no-cache", "helm-3~3.19.2", "kubectl"}).
		WithDirectory("/dagger-helm", chart).
		WithWorkdir("/dagger-helm")
}

func runInstallAssertions(ctx context.Context, engineName string, engineKind string, port int, kubectl *dagger.Container) error {
	// Helm can report success before the controller creates its first pod.
	for _, condition := range []string{"create", "condition=Ready"} {
		_, err := kubectl.WithExec([]string{
			"kubectl", "wait", "pod",
			"--selector=name=" + engineName,
			"--namespace=dagger",
			"--for=" + condition,
			"--timeout=5m",
		}).Sync(ctx)
		if err != nil {
			return fmt.Errorf("wait for engine pod %s (%s): %w", engineName, condition, err)
		}
	}

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
	stdout, err := kubectl.WithExec(withoutOuterSession("sh", "-c", command), dagger.ContainerWithExecOpts{
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

// withoutOuterSession wraps a command whose Dagger client must reach the
// Helm-installed engine configured by _EXPERIMENTAL_DAGGER_RUNNER_HOST.
func withoutOuterSession(args ...string) []string {
	return append([]string{"env", "-u", "DAGGER_SESSION_PORT", "-u", "DAGGER_SESSION_TOKEN"}, args...)
}
