// K3s Go examples module

package main

import (
	"context"
	"dagger/examples/internal/dagger"
	"time"
)

type Examples struct{}

// starts a k3s server and deploys a helm chart
func (m *Examples) K3S(ctx context.Context) (string, error) {
	k3s := dag.K3S("test")
	kServer := k3s.Server()

	kServer, err := kServer.Start(ctx)
	if err != nil {
		return "", err
	}

	ep, err := kServer.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 80, Scheme: "http"})
	if err != nil {
		return "", err
	}

	return dag.Container().From("alpine/helm").
		WithExec([]string{"apk", "add", "kubectl"}).
		WithEnvVariable("KUBECONFIG", "/.kube/config").
		WithFile("/.kube/config", k3s.Config()).
		WithExec([]string{"helm", "upgrade", "--install", "--force", "--wait", "--debug", "nginx", "oci://registry-1.docker.io/bitnamicharts/nginx"}).
		WithExec([]string{"sh", "-c", "while true; do curl -sS " + ep + " && exit 0 || sleep 1; done"}).Stdout(ctx)

}

// starts a k3s server with a local registry and a pre-loaded alpine image
func (m *Examples) K3SServer(ctx context.Context) (*dagger.Service, error) {
	regSvc := dag.Container().From("registry:2.8").
		WithExposedPort(5000).AsService()

	_, err := dag.Container().From("quay.io/skopeo/stable").
		WithServiceBinding("registry", regSvc).
		WithEnvVariable("BUST", time.Now().String()).
		WithExec([]string{"copy", "--dest-tls-verify=false", "docker://docker.io/alpine:latest", "docker://registry:5000/alpine:latest"}, dagger.ContainerWithExecOpts{UseEntrypoint: true}).Sync(ctx)
	if err != nil {
		return nil, err
	}

	return dag.K3S("test").With(func(k *dagger.K3S) *dagger.K3S {
		return k.WithContainer(
			k.Container().
				WithEnvVariable("BUST", time.Now().String()).
				WithExec([]string{"sh", "-c", `
cat <<EOF > /etc/rancher/k3s/registries.yaml
mirrors:
  "registry:5000":
    endpoint:
      - "http://registry:5000"
EOF`}).
				WithServiceBinding("registry", regSvc),
		)
	}).Server(), nil
}

// returns a kubectl container with the configured kube config context ready to run
// administrative commands
func (m *Examples) K3SKubectl(ctx context.Context, args string) *dagger.Container {
	return dag.K3S("test").Kubectl(args)
}
