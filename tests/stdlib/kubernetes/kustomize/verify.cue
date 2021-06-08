package main

import (
	"dagger.io/dagger/op"
	"dagger.io/dagger"
	"dagger.io/alpine"
)

#VerifyKustomize: {
	source: dagger.#Artifact

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: "=~5.1"
			}
		},

		// Check files
		op.#Exec & {
			always: true
			args: [
				"sh", "-c", "test $(ls /source | wc -l) = 1",
			]
			mount: "/source": from: source
		},

		// Check image tag kustomization
		op.#Exec & {
			always: true
			args: [
				"sh", "-c", #"""
						grep -q "\- image: nginx:v1" /source/result.yaml
					"""#,
			]
			mount: "/source": from: source
		},

		// Check replicas kustomization
		op.#Exec & {
			always: true
			args: [
				"sh", "-c", #"""
						grep -q "replicas: 2" /source/result.yaml
					"""#,
			]
			mount: "/source": from: source
		},

		// Check pod merge by kustomization
		op.#Exec & {
			always: true
			args: [
				"sh", "-c", #"""
						grep -q "kind: Pod" /source/result.yaml
					"""#,
			]
			mount: "/source": from: source
		},

		// Check pod name
		op.#Exec & {
			always: true
			args: [
				"sh", "-c", #"""
						grep -q "name: test-pod" /source/result.yaml
					"""#,
			]
			mount: "/source": from: source
		},
	]
}
