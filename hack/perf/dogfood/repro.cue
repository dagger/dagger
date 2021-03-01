package repro

import (
	"dagger.io/dagger"
	"dagger.io/go"
)

base: {
	repository: dagger.#Dir
	test: go.#Test & {
		source: repository
		packages: "./..."
	}
	build: go.#Build & {
					source:   test
					packages: "./cmd/dagger"
					output:   "/usr/local/bin/dagger"
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
	cmd1: {
					#dagger: {
									compute: [dagger.#Load & {
													from: help
									}, dagger.#Exec & {
													args: ["dagger", "compute", "-h"]
									}]
					}
	}
	cmd2: {
					#dagger: {
									compute: [dagger.#Load & {
													from: cmd1
									}, dagger.#Exec & {
													args: ["dagger", "compute", "-h"]
									}]
					}
	}
}

input: {
	repository: {
					#dagger: {
									compute: [{
													do:  "local"
													dir: "."
													include: []
									}]
					}
	}
}

output: {
	cmd2: {
					#dagger: {
									// Run a command with the binary we just built
									compute: [dagger.#Load & {
													from: cmd1
									}, dagger.#Exec & {
													args: ["dagger", "compute", "-h"]
									}]
					}
	}

	cmd1: {
					#dagger: {
									// Run a command with the binary we just built
									compute: [dagger.#Load & {
													from: help
									}, dagger.#Exec & {
													args: ["dagger", "compute", "-h"]
									}]
					}
	}

	help: {
					#dagger: {
									// Run a command with the binary we just built
									compute: [dagger.#Load & {
													from: build
									}, dagger.#Exec & {
													args: ["dagger", "-h"]
									}]
					}
	}

	build: {
					// Go version to use
					version: *"1.16" | string

					// Source Directory to build
					// source: input.repository
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
					env: [string]: string
					#dagger: {
									compute: [dagger.#Copy & {
													from: go.#Go & {
																	version: version
																	"source":  source
																	"env":     env
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
}
