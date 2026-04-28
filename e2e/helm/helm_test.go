// Package helm contains e2e contract tests for Dagger's Helm chart.
//
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
// workspace:include ../../sdk/go
// workspace:include ../../util
package helm

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
)

const (
	chartPath         = "helm/dagger"
	helmImage         = "cgr.dev/chainguard/wolfi-base"
	goImage           = "golang:1.25.6-alpine"
	testEngineImage   = "registry.dagger.io/engine:main"
	testEngineVersion = "v0.20.6"
)

var daggerCLISourceIncludes = []string{
	".changes/.next",
	"analytics/**",
	"cmd/dagger/**",
	"core/modules/**",
	"core/openrouter/**",
	"dagql/**",
	"engine/**",
	"go.mod",
	"go.sum",
	"internal/**",
	"sdk/go/**",
	"util/**",
}

func TestCustomProbes(t *testing.T) {
	ctx := t.Context()
	client := connect(t)

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

	out, err := helmContainer(client).
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
	client := connect(t)

	_, err := helmContainer(client).
		WithExec([]string{"helm", "package", "."}).
		Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstallK3S(t *testing.T) {
	ctx := t.Context()
	client := connect(t)

	k3s := newK3S(client, "helm-test")
	k3sSvc, err := k3s.server().Start(ctx)
	if err != nil {
		t.Fatalf("start k3s: %v", err)
	}
	t.Cleanup(func() {
		if _, err := k3sSvc.Stop(context.Background()); err != nil {
			t.Logf("stop k3s: %v", err)
		}
	})

	kubectl, err := helmContainer(client).
		WithMountedFile("/usr/bin/dagger", daggerCLIBinary(client)).
		WithServiceBinding("helm-test", k3sSvc).
		WithFile("/.kube/config", k3s.config(client, false)).
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

	client, err := dagger.Connect(t.Context())
	if err != nil {
		t.Fatalf("connect to dagger: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close dagger client: %v", err)
		}
	})
	return client
}

func helmContainer(client *dagger.Client) *dagger.Container {
	chart := client.CurrentWorkspace().
		Directory("/", dagger.WorkspaceDirectoryOpts{Include: []string{chartPath}}).
		Directory(chartPath)

	return client.Container().
		From(helmImage).
		WithExec([]string{"apk", "add", "--no-cache", "helm~3.18.4", "kubectl"}).
		WithDirectory("/dagger-helm", chart).
		WithWorkdir("/dagger-helm")
}

func daggerCLIBinary(client *dagger.Client) *dagger.File {
	source := client.CurrentWorkspace().
		Directory("/", dagger.WorkspaceDirectoryOpts{Include: daggerCLISourceIncludes})

	return client.Container().
		From(goImage).
		WithExec([]string{"apk", "add", "--no-cache", "git"}).
		WithEnvVariable("CGO_ENABLED", "0").
		WithMountedCache("/go/pkg/mod", client.CacheVolume("e2e-helm-go-mod")).
		WithMountedCache("/root/.cache/go-build", client.CacheVolume("e2e-helm-go-build")).
		WithDirectory("/src", source).
		WithWorkdir("/src").
		WithExec([]string{
			"go", "build",
			"-ldflags", "-s -w -X github.com/dagger/dagger/engine.Version=" + testEngineVersion + " -X github.com/dagger/dagger/engine.Tag=main",
			"-o", "/out/dagger",
			"./cmd/dagger",
		}).
		File("/out/dagger")
}

type k3sCluster struct {
	name          string
	configCache   *dagger.CacheVolume
	enableTraefik bool
	container     *dagger.Container
}

func newK3S(client *dagger.Client, name string) *k3sCluster {
	configCache := client.CacheVolume("k3s_config_" + name)
	ctr := client.Container().
		From("rancher/k3s:latest").
		WithNewFile("/usr/bin/entrypoint.sh", k3sEntrypoint, dagger.ContainerWithNewFileOpts{
			Permissions: 0o755,
		}).
		WithEntrypoint([]string{"entrypoint.sh"}).
		WithMountedCache("/etc/rancher/k3s", configCache).
		WithMountedTemp("/etc/lib/cni").
		WithMountedTemp("/var/lib/kubelet").
		WithMountedCache("/var/lib/rancher", client.CacheVolume("k3s_cache_"+name)).
		WithEnvVariable("CACHEBUST", time.Now().String()).
		WithExec([]string{"rm", "-rf", "/var/lib/rancher/k3s/server/tls", "/etc/rancher/k3s/k3s.yaml"}).
		WithExec([]string{"rm", "-rf", "/var/lib/rancher/k3s/"}).
		WithMountedTemp("/var/log").
		WithExposedPort(6443)

	return &k3sCluster{
		name:        name,
		configCache: configCache,
		container:   ctr,
	}
}

func (k *k3sCluster) server() *dagger.Service {
	traefikFlags := ""
	if !k.enableTraefik {
		traefikFlags = "--disable traefik "
	}
	return k.container.AsService(dagger.ContainerAsServiceOpts{
		Args: []string{
			"sh", "-c",
			"k3s server --debug --bind-address $(ip route | grep src | awk '{print $NF}') " + traefikFlags + "--disable metrics-server --egress-selector-mode=disabled",
		},
		InsecureRootCapabilities: true,
		UseEntrypoint:            true,
	})
}

func (k *k3sCluster) config(client *dagger.Client, local bool) *dagger.File {
	ctr := client.Container().
		From("alpine").
		WithEnvVariable("CACHE", time.Now().String()).
		WithMountedCache("/cache/k3s", k.configCache).
		WithExec([]string{"sh", "-c", `while [ ! -f "/cache/k3s/k3s.yaml" ]; do echo "k3s.yaml not ready, is server started?. waiting.. " && sleep 0.5; done`}).
		WithExec([]string{"cp", "/cache/k3s/k3s.yaml", "k3s.yaml"})
	if local {
		ctr = ctr.WithExec([]string{"sed", "-i", `s/https:.*:6443/https:\/\/localhost:6443/g`, "k3s.yaml"})
	}
	return ctr.File("k3s.yaml")
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

const k3sEntrypoint = `#!/bin/sh

set -o errexit
set -o nounset

#########################################################################################################################################
# DISCLAIMER																																																														#
# Copied from https://github.com/moby/moby/blob/ed89041433a031cafc0a0f19cfe573c31688d377/hack/dind#L28-L37															#
# Permission granted by Akihiro Suda <akihiro.suda.cz@hco.ntt.co.jp> (https://github.com/k3d-io/k3d/issues/493#issuecomment-827405962)	#
# Moby License Apache 2.0: https://github.com/moby/moby/blob/ed89041433a031cafc0a0f19cfe573c31688d377/LICENSE														#
#########################################################################################################################################
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
  echo "[$(date -Iseconds)] [CgroupV2 Fix] Evacuating Root Cgroup ..."
  # move the processes from the root group to the /init group,
  # otherwise writing subtree_control fails with EBUSY.
  mkdir -p /sys/fs/cgroup/init
  xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init/cgroup.procs || :
  # enable controllers
  sed -e 's/ / +/g' -e 's/^/+/' <"/sys/fs/cgroup/cgroup.controllers" >"/sys/fs/cgroup/cgroup.subtree_control"
  echo "[$(date -Iseconds)] [CgroupV2 Fix] Done"
fi

exec "$@"
`
