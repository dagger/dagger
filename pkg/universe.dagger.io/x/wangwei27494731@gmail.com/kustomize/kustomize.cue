package kustomize

import (
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

// Kustomize and output kubernetes manifest
#Kustomize: {
	// Kubernetes source
	source: dagger.#FS

	// Optional Kustomization file
	kustomization: string | dagger.#FS

	_writeYaml: output: dagger.#FS

	if (kustomization & string) != _|_ {
		_writeYaml: core.#WriteFile & {
			input:    dagger.#Scratch
			path:     "kustomization.yaml"
			contents: kustomization
		}
	}

	_baseImage: #Image

	run: bash.#Run & {
		input: *_baseImage.output | docker.#Image
		mounts: {
			if (kustomization & string) != _|_ {
				"kustomization.yaml": {
					contents: _writeYaml.output
					dest:     "/kustom"
				}
			}
			if (kustomization & string) == _|_ {
				"kustomization.yaml": {
					contents: kustomization
					dest:     "/kustom"
				}
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
