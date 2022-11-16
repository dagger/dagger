package dockerprovision

import (
	"dagger.io/dagger/internal/engineconn"
)

const (
	DockerImageConnName     = "docker-image"
	DockerContainerConnName = "docker-container"
)

func init() {
	engineconn.Register(DockerImageConnName, NewDockerImage)
	engineconn.Register(DockerContainerConnName, NewDockerContainer)
}

const (
	// trim image digests to 16 characters to makeoutput more readable
	digestLen                       = 16
	containerNamePrefix             = "dagger-engine-"
	engineSessionBinPrefix          = "dagger-engine-session-"
	containerEngineSessionBinPrefix = "/usr/bin/" + engineSessionBinPrefix
)
