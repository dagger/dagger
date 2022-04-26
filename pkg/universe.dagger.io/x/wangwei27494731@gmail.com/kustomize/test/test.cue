package kustomize

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/x/wangwei27494731@gmail.com/kustomize"
	"encoding/yaml"
	"universe.dagger.io/bash"
)

dagger.#Plan & {
	client: filesystem: "./test/testdata": read: contents: dagger.#FS
	actions: test: {
		// Run Kustomize
		kustom: kustomize.#Kustomize & {
			source:        client.filesystem."./test/testdata".read.contents
			kustomization: yaml.Marshal({
				resources: ["deployment.yaml", "pod.yaml"]
				images: [{
					name:   "nginx"
					newTag: "v1"
				}]
				replicas: [{
					name:  "nginx-deployment"
					count: 2
				}]
			})
		}

		_baseImage: #Image

		_file: core.#WriteFile & {
			input:    dagger.#Scratch
			path:     "/result.yaml"
			contents: kustom.output
		}

		run: bash.#Run & {
			input: _baseImage.output
			script: contents: #"""
				cat /result/result.yaml
				grep -q "replicas: 2" /result/result.yaml
				"""#
			mounts: "/result": {
				dest:     "/result"
				contents: _file.output
			}
		}

		// Test for kustomization FS type
		kustomFS: kustomize.#Kustomize & {
			source:        client.filesystem."./test/testdata".read.contents
			kustomization: client.filesystem."./test/testdata".read.contents
		}

		_fileFS: core.#WriteFile & {
			input:    dagger.#Scratch
			path:     "/result.yaml"
			contents: kustomFS.output
		}

		runFS: bash.#Run & {
			input: _baseImage.output
			script: contents: #"""
				cat /result/result.yaml
				grep -q "replicas: 2" /result/result.yaml
				"""#
			mounts: "/result": {
				dest:     "/result"
				contents: _fileFS.output
			}
		}

	}
}
