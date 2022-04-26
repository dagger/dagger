package kustomize

import (
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

#Image: {
	// Kustomize binary version
	version: *"3.8.7" | string

	_build: docker.#Build & {
		steps: [
			alpine.#Build & {
				packages: {
					bash: {}
					curl: {}
				}
			},
			bash.#Run & {
				env: VERSION: version
				script: contents: #"""
					# download Kustomize binary
					curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash -s $VERSION && mv kustomize /usr/local/bin
					"""#
			},
		]
	}

	output: _build.output
}
