package helm

import (
	"fmt"
	"time"

	dagger "github.com/dagger/dagger/e2e/helm/dagger"
)

type k3sCluster struct {
	configCache *dagger.CacheVolume
	service     *dagger.Service
	config      *dagger.File
}

func newK3S(dag *dagger.Client, name string) k3sCluster {
	kubeconfigName := fmt.Sprintf("k3s-%d.yaml", time.Now().UnixNano())
	kubeconfigPath := "/etc/rancher/k3s/" + kubeconfigName
	kubeconfigCachePath := "/cache/k3s/" + kubeconfigName
	waitForKubeconfig := fmt.Sprintf(`while [ ! -f "%s" ]; do echo "%s not ready, is server started?. waiting.. " && sleep 0.5; done`, kubeconfigCachePath, kubeconfigName)
	k3s := k3sCluster{
		configCache: dag.CacheVolume("k3s_config_" + name),
	}

	k3s.service = dag.Container().
		From("rancher/k3s:latest").
		// Keep this asset tied to the workspace, not CurrentModule, so the
		// client-only dagger.json does not become part of the file-loading contract.
		// This should be "e2e/helm/k3s-entrypoint.sh"; until
		// https://github.com/dagger/dagger/pull/13053 lands here, the path must be
		// current-workspace relative.
		WithFile("/usr/bin/entrypoint.sh", dag.CurrentWorkspace().File("k3s-entrypoint.sh"), dagger.ContainerWithFileOpts{
			Permissions: 0o755,
		}).
		WithEntrypoint([]string{"entrypoint.sh"}).
		WithMountedCache("/etc/rancher/k3s", k3s.configCache).
		WithMountedTemp("/etc/lib/cni").
		WithMountedTemp("/var/lib/kubelet").
		WithMountedCache("/var/lib/rancher", dag.CacheVolume("k3s_cache_"+name)).
		WithEnvVariable("CACHEBUST", time.Now().String()).
		WithExec([]string{"rm", "-rf", "/var/lib/rancher/k3s/", "/etc/rancher/k3s/k3s.yaml", kubeconfigPath}).
		WithMountedTemp("/var/log").
		WithExposedPort(6443).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{
				"sh", "-c",
				"k3s server --debug --bind-address $(ip route | grep src | awk '{print $NF}') --write-kubeconfig " + kubeconfigPath + " --disable traefik --disable metrics-server --egress-selector-mode=disabled",
			},
			InsecureRootCapabilities: true,
			UseEntrypoint:            true,
		})

	k3s.config = dag.Container().
		From("alpine").
		WithEnvVariable("CACHE", time.Now().String()).
		WithMountedCache("/cache/k3s", k3s.configCache).
		WithExec([]string{"sh", "-c", waitForKubeconfig}).
		WithExec([]string{"cp", kubeconfigCachePath, "k3s.yaml"}).
		File("k3s.yaml")

	return k3s
}
