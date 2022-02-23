package main

import (
	"dagger.io/dagger"
	// "alpha.dagger.io/dagger/op"
	// "alpha.dagger.io/alpine"
)

dagger.#Plan & {
	// should fail because of misspelled value
	proxy: dockerSocket: unix: "/var/run/docker.soc"

	actions: {
		image: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		imageWithDocker: dagger.#Exec & {
			input: image.output
			args: ["apk", "add", "--no-cache", "docker-cli"]
		}

		verify: dagger.#Exec & {
			input: imageWithDocker.output
			mounts: docker: {
				dest:     "/var/run/docker.sock"
				contents: proxy.dockerSocket.service
			}
			args: ["docker", "info"]
		}
	}
}
