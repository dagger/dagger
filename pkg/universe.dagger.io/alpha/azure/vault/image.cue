package vault

import (
	"dagger.io/dagger/core"
	"universe.dagger.io/docker"
)

#Image: docker.#Build & {
	_src: core.#Source & {
		path: "."
	}
	steps: [
		docker.#Pull & {
			source: "alpine"
		},
		docker.#Run & {
			command: {
				name: "apk"
				args: ["add", "jq", "curl"]
				flags: {
					"--no-cache": true
					"--update":   true
				}
			}
		},
		docker.#Copy & {
			contents: _src.output
			source:   "get-secret.sh"
			dest:     "/usr/local/bin/get-secret"
		},
	]
}
