package docker

import (
	"dagger.io/dagger/op"
	"dagger.io/alpine"
)

#Client: #up: [
	op.#Load & {
		from: alpine.#Image & {
			package: bash:             true
			package: jq:               true
			package: curl:             true
			package: "openssh-client": true
			package: "docker-cli":     true
		}
	},
]
