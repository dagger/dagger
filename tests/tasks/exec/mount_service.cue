package main

import (
	"alpha.dagger.io/europa/dagger/engine"
)

engine.#Plan & {
	proxy: dockerSocket: unix: "/var/run/docker.sock"

	actions: {
		image: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		imageWithDocker: engine.#Exec & {
			input: image.output
			args: ["apk", "add", "--no-cache", "docker-cli"]
		}

		verify: engine.#Exec & {
			input: imageWithDocker.output
			mounts: docker: {
				dest:     "/var/run/docker.sock"
				contents: proxy.dockerSocket.service
			}
			args: ["docker", "info"]
		}
	}
}
