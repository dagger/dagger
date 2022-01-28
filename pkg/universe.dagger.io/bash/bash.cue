// Helpers to run bash commands in containers
package bash

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

// Run a bash script in a Docker container
#Run: {
	// Source directory containing one or more bash scripts
	source: dagger.#FS

	// Optional arguments to the script
	args: [...string]

	{
		script:    string
		_mkSource: engine.#WriteFile & {
			input:    engine.#Scratch
			path:     "run.sh"
			contents: script
		}
		source:   _mkSource.output
		filename: "run.sh"
	} | {}

	// Where to mount the source directory
	mountpoint: "/bash/source"
	// Filename of the script to execute, relative to source
	filename: string

	container: docker.#Run & {
		// FIXME: why does this not work if moved to outer scope?
		_buildDefaultImage: alpine.#Build & {packages: bash: _}
		image:              _ | *_buildDefaultImage.output
		command: {
			name:   "bash"
			"args": ["\(mountpoint)/\(filename)"] + args
			flags: {
				"--norc": true
				"-e":     true
				"-o":     "pipefail"
			}
		}
		mounts: "Bash source": {
			contents: source
			dest:     mountpoint
		}
	}
}

// A ready-to-use Docker image with bash installed
