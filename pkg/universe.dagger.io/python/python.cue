// Helpers to run python programs
package python

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

// Run a python script in a container
#Run: {
	// Contents of the python script
	script: {
		// A directory containing one or more bash scripts
		directory: dagger.#FS

		// Name of the file to execute
		filename: string

		_directory: directory
		_filename:  filename
	} | {
		// Script contents
		contents: string

		_filename: "run.py"
		_write:    core.#WriteFile & {
			input:      dagger.#Scratch
			path:       _filename
			"contents": contents
		}
		_directory: _write.output
	}

	// arguments to the script
	args: [...string]

	// where to mount the script inside the container
	_mountpoint: "/run/python"

	docker.#Run & {
		command: {
			name:   string | *"python3"
			"args": ["\(_mountpoint)/\(script._filename)"] + args
		}

		// As a convenience, image defaults to a ready-to-use python environment
		input: docker.#Image | *_defaultImage.output

		_defaultImage: alpine.#Build & {
			packages: python: version: "3"
		}

		mounts: "Python script": {
			contents: script._directory
			dest:     _mountpoint
		}
	}
}
