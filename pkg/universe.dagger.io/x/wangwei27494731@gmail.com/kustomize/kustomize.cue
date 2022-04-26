package kustomize

import (
	"universe.dagger.io/bash"
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

// Apply a Kubernetes Kustomize folder
#Kustomize: {
	// Kubernetes source
	source: dagger.#FS

	// Optional Kustomization file
	kustomization: string

	// Kubeconfig
	kubeconfig: dagger.#Secret

	_baseImage: #Image

	_writeYaml: output: core.#FS

	_writeYaml: core.#WriteFile & {
		input:    dagger.#Scratch
		path:     "kustomization.yaml"
		contents: kustomization
	}

	run: bash.#Run & {
		input: _baseImage.output
		mounts: {
			"kustomization.yaml": {
				contents: _writeYaml.output
				dest:     "/kustom"
			}
			"/root/.kube/config": {
				dest:     "/root/.kube/config"
				type:     "secret"
				contents: kubeconfig
			}
			"/source": {
				dest:     "/source"
				contents: source
			}
		}
		script: contents: #"""
			cp /kustom/kustomization.yaml /source | true
			mkdir -p /output
			kustomize build /source >> /output/result.yaml
			"""#
		export: files: "/output/result.yaml": string
	}

	output: run.export.files."/output/result.yaml"
}
