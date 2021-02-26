package main

import (
	"dagger.io/dagger"
	"dagger.io/go"
)

helpFromDocker: {
				#dagger: {
								// Run a command in the docker image we just built
								compute: [dagger.#Load & {
												from: buildWithDocker
								}, dagger.#Exec & {
												args: ["dagger", "-h"]
								}]
				}
}
buildWithDocker: {
				#dagger: {
								compute: [dagger.#DockerBuild & {
												context: repository
								}]
				}
}
help: {
				#dagger: {
								compute: [dagger.#Load & {
												from: build
								}, dagger.#Exec & {
												args: ["dagger", "-h"]
								}]
				}
}
test: {
				// Go version to use
				version: *"1.16" | string

				// Source Directory to build
				source: {
								#dagger: {
												compute: [dagger.#Op & dagger.#Op & dagger.#Op & {
																do:  "local"
																dir: "."
																include: []
												}]
								}
				}

				// Packages to test
				packages: "./..."

				// Arguments to the Go binary
				args: ["test", "-v", packages & string]

				// Environment variables
				env: {}
				#dagger: {
								compute: [dagger.#FetchContainer & {
												ref: "docker.io/golang:\(version)-alpine"
								}, dagger.#Exec & {
												args: ["go"] + args
												// FIXME: this should come from the golang image.
												// https://github.com/dagger/dagger/issues/130
												env: env & {
																CGO_ENABLED: "0"
																PATH:        "/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
																GOPATH:      "/go"
												}
												dir: "/src"
												mount: {
																"/src": {
																				from: source
																}
																"/root/.cache": "cache"
												}
								}]
				}
}

// Build `dagger` using Go
build: {
				// Go version to use
				version: *"1.16" | string

				// Source Directory to build
				source: {
								#dagger: {
												compute: [dagger.#Op & dagger.#Op & {
																do:  "local"
																dir: "."
																include: []
												}]
								}
				}

				// Packages to build
				packages: "./cmd/dagger"

				// Target architecture
				arch: *"amd64" | string

				// Target OS
				os: *"linux" | string

				// Build tags to use for building
				tags: *"" | string

				// LDFLAGS to use for linking
				ldflags: *"" | string

				// Specify the targeted binary name
				output: "/usr/local/bin/dagger"
				env: {}
				#dagger: {
								compute: [dagger.#Copy & {
												from: go.#Go & {
																version: version
																source:  source
																env:     env
																args: ["build", "-v", "-tags", tags, "-ldflags", ldflags, "-o", output, packages]
												}
												src:  output
												dest: output
								}]
				}
}
repository: {
				#dagger: {
								compute: [dagger.#Op & {
												do:  "local"
												dir: "."
												include: []
								}]
				}
}
