package main

import (
	"universe.dagger.io/docker"
)

let GoVersion = "1.17"
let GolangCILintVersion = "1.44.0"

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

	// base image for the Go linter
	// https://golangci-lint.run/usage/install/#docker
	goLinter: docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "index.docker.io/golangci/golangci-lint:v\(GolangCILintVersion)"
			},
		]
	}
}
