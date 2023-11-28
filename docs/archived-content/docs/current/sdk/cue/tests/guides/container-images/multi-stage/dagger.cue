package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
	"universe.dagger.io/go"
)

dagger.#Plan & {
	client: filesystem: "./src": read: contents: dagger.#FS

	actions: {
		// Build app in a "golang" container image.
		build: go.#Build & {
			source: client.filesystem."./src".read.contents
		}

		// Build lighter image,
		// without app's build dependencies.
		run: docker.#Build & {
			steps: [
				alpine.#Build & {
					packages: "ca-certificates": _
				},
				// This is the important part, it works like
				// `COPY --from=build /output /opt` in a Dockerfile.
				docker.#Copy & {
					contents: build.output
					dest:     "/opt"
				},
				docker.#Set & {
					config: cmd: ["/opt/testmulti"]
				},
			]
		}

		push: docker.#Push & {
			image: run.output
			dest:  "registry.example.com/app"
		}
	}
}
