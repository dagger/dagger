package compose

import (
	"dagger.io/alpine"
)

// A container image to run the docker-compose client
#Client: alpine.#Image & {
	package: {
		bash:             true
		jq:               true
		curl:             true
		"openssh-client": true
		"docker-compose": true
	}
}
