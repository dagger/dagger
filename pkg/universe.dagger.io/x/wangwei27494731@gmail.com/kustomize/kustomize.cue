package kustomize

import (
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

// Apply a Kubernetes Kustomize folder
#Kustomize: {
	// Kubernetes source
	source: string

	// Optional Kustomization file
	kustomization: string

	// Kubeconfig
	kubeconfig: dagger.#Secret

	_baseImage: #Image

	_file: core.#Source & {
		path: source
	}

	_copy: docker.#Copy & {
		input:    _baseImage.output
		contents: _file.output
		dest:     "/source"
	}

	_writeYaml: output: core.#FS

	_writeYaml: core.#WriteFile & {
		input:    dagger.#Scratch
		path:     "kustomization.yaml"
		contents: kustomization
	}

	_writeYamlOutput: _writeYaml.output

	run: bash.#Run & {
		input: _copy.output
		mounts: "kustomization.yaml": {
			contents: _writeYamlOutput
			dest:     "/kustom"
		}
		mounts: "/root/.kube/config": {
			dest:     "/root/.kube/config"
			type:     "secret"
			contents: kubeconfig
		}
		script: contents: #"""
			cp /kustom/kustomization.yaml /source | true
			kustomize build /source | kubectl apply -f -
			"""#
	}
}
