package bats

import (
	"strings"

	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
	"alpha.dagger.io/alpine"
)

#Bats: {
	// Source containing bats files
	source: dagger.#Artifact & dagger.#Input

	// bats options
	options: [...string]

	// mount points passed to the bats container
	mount: [string]: from: dagger.#Artifact

	// environment variables
	env: [string]: string

	// socket mounts for the bats container
	socket: [string]: dagger.#Stream

	// setup commands to run only once (for installing dependencies)
	setupCommands: [...string]

	// init script to run right before bats
	initScript: string | *""

	defaultOptions: ["--print-output-on-failure", "--show-output-of-passing-tests"]

	ctr: os.#Container & {
		image: alpine.#Image & {
			package: curl: true
			package: bash: "~=5.1"
			package: jq:   "~=1.6"
			package: npm:  true
			package: git:  true
		}
		shell: path: "/bin/bash"
		setup:   ["npm install -g bats"] + setupCommands
		command: #"""
            \#(initScript)
            bats \#(strings.Join(defaultOptions, " ")) \#(strings.Join(options, " ")) ../src
            """#

		dir:     "/app"
		"mount": mount
		"mount": "/src": from: source
		"env":    env
		"socket": socket
	}
}
