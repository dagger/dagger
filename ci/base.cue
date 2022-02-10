package main

import (
	"universe.dagger.io/docker"
)

let GoVersion = "1.17"

// Base container images used for the CI
images: {

	// base image to build go binaries
	goBuilder: docker.#Build & {
		_packages: ["bash", "git"]

		steps: [
			docker.#Pull & {
				source: "index.docker.io/golang:\(GoVersion)-alpine"
			},
			for pkg in _packages {
				docker.#Run & {
					command: {
						name: "apk"
						args: ["add", pkg]
						flags: {
							"-U":         true
							"--no-cache": true
						}
					}
				}
			},
		]
	}
}
