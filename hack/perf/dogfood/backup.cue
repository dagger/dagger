package repro

import (
	"dagger.io/dagger"
	"dagger.io/go"
)

base: {
	repository: dagger.#Dir

	build: go.#Build & {
		source:   repository
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
	help: {
		#dagger: {
			compute: [dagger.#Load & {
				from: build
			}, dagger.#Exec & {
				args: ["dagger", "-h"]
			}]
		}
	}

	build: {
	  version: *"1.16" | string
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
