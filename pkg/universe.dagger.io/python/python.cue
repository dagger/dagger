// Helpers to run python programs
package python

import (
	"universe.dagger.io/docker"

	"universe.dagger.io/alpine"
)

// Run a python script in a container
#Run: docker.#Run & {
	script: string
	command: {
		name: "python"
		flags: "-c": script
	}

	// As a convenience, image defaults to a ready-to-use python environment
	image: docker.#Image | *_defaultImage

	_defaultImage: alpine.#Image & {
		packages: python: version: "3"
	}
}
