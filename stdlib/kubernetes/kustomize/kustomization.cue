package kustomize

import (
	"dagger.io/dagger/op"
	"dagger.io/dagger"
	"dagger.io/alpine"
)

#Kustomization: {
	// Kustomize binary version
	version: *"v3.8.7" | string

	#code: #"""
		[ -e /usr/local/bin/kubectl ] || {
			curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash && mv kustomize /usr/local/bin
		}
		"""#

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: "=~5.1"
				package: jq:   "=~1.6"
				package: curl: "=~7.76"
			}
		},

		op.#WriteFile & {
			dest:    "/entrypoint.sh"
			content: #code
		},

		op.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/entrypoint.sh",
			]
		},
	]
}

// Apply a Kubernetes Kustomize folder
#Kustomize: {
	// Kubernetes source
	source: dagger.#Artifact

	// Optional Kustomization file
	kustomization: string

	// Kustomize binary version
	version: *"v3.8.7" | string

	#code: #"""
		cp /kustomization.yaml /source | true
		mkdir -p /output
		kustomize build /source >> /output/result.yaml
		"""#

	#up: [
		op.#Load & {
			from: #Kustomization & {"version": version}
		},

		op.#WriteFile & {
			dest:    "/entrypoint.sh"
			content: #code
		},

		if kustomization != _|_ {
			op.#WriteFile & {
				dest:    "/kustomization.yaml"
				content: kustomization
				mode:    0o600
			}
		},

		op.#Exec & {
			always: true
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/entrypoint.sh",
			]
			mount: "/source": from: source
		},

		op.#Subdir & {
			dir: "/output"
		},
	]
}
