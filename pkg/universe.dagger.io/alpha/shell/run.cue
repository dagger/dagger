// Helpers to run shell commands in containers
package shell

import (
	"list"

	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/docker"
)

// Like #Run, but with a pre-configured container image.
#RunSimple: #Run & {
	_simpleImage: #Image
	input:        _simpleImage.output
}

// Default simple container image which can run a shell
#Image: docker.#Pull & {
	source: docker.#Ref | *"alpine:latest"
}

// Run a shell script in a Docker container
//  Since this is a thin wrapper over docker.#Run, we embed it.
//  Whether to embed or wrap is a case-by-case decision, like in Go.
#Run: {
	// Ash is the default because #Image is alpine
	shell: string | *"ash"

	// The script to execute
	script: {
		// A directory containing one or more shell scripts
		directory: dagger.#FS

		// Name of the file to execute
		filename: string

		_directory: directory
		_filename:  filename
	} | {
		// Script contents
		contents: string

		_filename: "run.sh"
		_write:    core.#WriteFile & {
			input:      dagger.#Scratch
			path:       _filename
			"contents": contents
		}
		_directory: _write.output
	}

	// Arguments to the script
	args: [...string]
	flags: [string]: string | bool

	_flatFlags: list.FlattenN([
			for k, v in flags {
			if (v & bool) != _|_ {
				[k]
			}
			if (v & string) != _|_ {
				[k, v]
			}
		},
	], 1)

	// Where in the container to mount the scripts directory
	_mountpoint: "/shell/scripts"

	docker.#Run & {
		// ignore entrypoint from image config as it can
		// create issues if it's not exec'ing to "$@"
		entrypoint: []
		command: {
			name:   shell
			"args": ["\(_mountpoint)/\(script._filename)"] + args + _flatFlags
			// FIXME: make default flags overrideable
			flags: {
				"--norc": true
				"-e":     true
				"-o":     "pipefail"
			}
		}
		mounts: "Shell scripts": {
			contents: script._directory
			dest:     _mountpoint
		}
	}
}
