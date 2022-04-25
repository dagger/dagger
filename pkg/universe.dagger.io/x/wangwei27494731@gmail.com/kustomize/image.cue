package kustomize

import (
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

#Image: {
	// Kustomize binary version
	version: *"3.8.7" | string

	// Kubectl version
	kubectlVersion: *"v1.23.5" | string

	_build: docker.#Build & {
		steps: [
			alpine.#Build & {
				packages: {
					bash: {}
					curl: {}
				}
			},
			bash.#Run & {
				env: {
					VERSION:   version
					K_VERSION: kubectlVersion
				}
				script: contents: #"""
					# download Kustomize binary
					curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash -s $VERSION && mv kustomize /usr/local/bin
					
					# download kubectl binary
					curl -LO https://dl.k8s.io/release/$K_VERSION/bin/linux/amd64/kubectl && chmod +x kubectl && mv kubectl /usr/local/bin/
					"""#
			},
		]
	}
	output: _build.output
}
