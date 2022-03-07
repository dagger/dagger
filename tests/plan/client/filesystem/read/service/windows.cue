package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: filesystem: "//./pipe/docker_engine": read: contents: dagger.#Service

	actions: {
		image: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		imageWithDocker: dagger.#Exec & {
			input: image.output
			args: ["apk", "add", "--no-cache", "docker-cli"]
		}

		test: dagger.#Exec & {
			input: imageWithDocker.output
			mounts: docker: {
				dest:     "/var/run/docker.sock"
				contents: client.filesystem."//./pipe/docker_engine".read.contents
			}
			args: ["docker", "info"]
		}
	}
}
