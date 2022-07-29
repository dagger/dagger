package auth

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
			source:   "azlogin.sh"
			dest:     "/usr/local/bin/azlogin"
		},
	]
}
