package test

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		build: docker.#Build & {
			steps: [
				docker.#Dockerfile & {
					dockerfile: contents: """
							FROM alpine:3.15

							RUN echo -n hello world >> /test.txt
						"""
				},
				docker.#Run & {
					script: """
						  # Verify that docker.#Dockerfile correctly connect output
						  # into other steps
							grep -q "hello world" /test.txt
						"""
				},
			]
		}

		verify: engine.#ReadFile & {
			input: build.output.rootfs
			path:  "/test.txt"
		} & {
			contents: "hello world"
		}
	}
}
