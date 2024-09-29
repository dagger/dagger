package main

import (
	"fmt"

	"github.com/dagger/dagger/sdk/dotnet/dev/internal/dagger"
)

const dockerEngineVersion = "27"

func dockerEngine() *dagger.Service {
	// Copied some if it from https://github.com/shykes/daggerverse/blob/main/docker/main.go.
	return dag.Container().
		From(fmt.Sprintf("docker:%s-dind", dockerEngineVersion)).
		WithoutEntrypoint().
		WithExec([]string{
			"dockerd",
			"--host=tcp://0.0.0.0:2375",
			"--host=unix:///var/run/docker.sock",
			"--tls=false",
		}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities:      true,
			ExperimentalPrivilegedNesting: true,
		}).
		WithExposedPort(2375).
		AsService()
}

func dockerCli() *dagger.Container {
	return dag.Container().
		From(fmt.Sprintf("docker:%s-cli", dockerEngineVersion))

}

func installDockerCli(ctr *dagger.Container) *dagger.Container {
	return ctr.WithFile(
		"/usr/local/bin/docker",
		dockerCli().File("/usr/local/bin/docker"),
	)
}

func dockerd(ctr *dagger.Container) *dagger.Container {
	return ctr.WithServiceBinding("dockerd", dockerEngine()).
		WithEnvVariable("DOCKER_HOST", "tcp://dockerd:2375")
}
