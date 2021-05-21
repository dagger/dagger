package docker

import (
	"dagger.io/dagger/op"
	"dagger.io/alpine"
)

#Client: {
	// Docker CLI version
	version: *"20.10.6" | string

	#Code: #"""
	curl -fsSL https://download.docker.com/linux/static/stable/x86_64/docker-\#(version).tgz | tar zxvf - --strip 1 -C /usr/bin docker/docker
	"""#

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash:             true
				package: jq:               true
				package: curl:             true
				package: "openssh-client": true
			}
		},

		op.#WriteFile & {
			content: #Code
			dest:    "/entrypoint.sh"
		},

		op.#Exec & {
			args: [
				"/bin/sh",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/entrypoint.sh",
			]
		},
	]
}
