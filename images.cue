package main

import (
	"universe.dagger.io/docker"
)

let GoVersion = "1.17"
let GolangCILintVersion = "1.44.0"
let CUEVersion = "0.4.2"

// Base container images used for the CI
#Images: {

	// base image to build go binaries
	goBuilder:  _goBuilder.output
	_goBuilder: docker.#Build & {
		_packages: ["bash", "git", "alpine-sdk"]

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
	goLinter:  _goLinter.output
	_goLinter: docker.#Pull & {
		source: "index.docker.io/golangci/golangci-lint:v\(GolangCILintVersion)"
	}

	// base image for CUE cli + alpine distrib
	cue: _cue._alpine.output
	_cue: {
		_cueBinary: docker.#Pull & {
			source: "index.docker.io/cuelang/cue:\(CUEVersion)"
		}

		_alpine: docker.#Build & {
			_packages: ["bash", "git"]

			steps: [
				docker.#Pull & {
					source: "index.docker.io/alpine:3"
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
				docker.#Copy & {
					// input:    _alpine.output
					contents: _cueBinary.output.rootfs
					source:   "/usr/bin/cue"
					dest:     "/usr/bin/cue"
				},
			]
		}
	}
}
