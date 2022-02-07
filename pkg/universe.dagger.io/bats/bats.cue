// Execute bats test suite
// https://github.com/sstephenson/bats
package bats

import (
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
)

//Todo: add socket integration
#Bats: {
	// Source containing bats files
	source: dagger.#FS

	// bats options
	options: [...string]

	// mount points passed to the bats container
	mounts: [name=string]: engine.#Mount

	// environment variables
	env: [string]: string

	// setup commands to run only once (for installing dependencies)
	setupCommands: [...string]

	// init script to run right before bats
	initScript: string | *""

	defaultOptions: ["--print-output-on-failure", "--show-output-of-passing-tests"]

	_build: docker.#Build & {
		steps: [
			alpine.#Build & {
				packages: {
					bash: {}
					curl: {}
					jq: {}
					npm: {}
					git: {}
				}
			},
			docker.#Run & {
				command: {
					name: "npm"
					args: ["install", "-g", "bats"] + setupCommands
				}
			},
		]
	}

	command: docker.#Run & {
		image: _build.output

		command: {
			name: "/bin/bash"
			flags: "-c": #"""
				\#(initScript)
				bats \#(strings.Join(defaultOptions, " ")) \#(strings.Join(options, " ")) ../src
				"""#
		}

		workdir: "/app"
		"mounts": {
			"Tests source": {
				dest:     "/src"
				contents: source
			}
			mounts
		}
		"env": env
	}
}
