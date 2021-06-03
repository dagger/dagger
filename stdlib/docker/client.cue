package docker

import (
	"dagger.io/alpine"
)

// A container image to run the Docker client
#Client: alpine.#Image & {
	package: {
		bash:             true
		jq:               true
		curl:             true
		"openssh-client": true
		"docker-cli":     true
	}
}
