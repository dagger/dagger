package build

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/docker"
)

// Modular build API for Docker containers
#Build: {
	steps: [#Step, ...#Step]
	output: docker.#Image

	// Generate build DAG from linerar steps
	dag: {
		for idx, step in steps {
			"\(idx)": step & {
				// connect input to previous output
				if idx > 0 {
					input: dag["\(idx-1)"].output
				}
			}
		}
	}

	if len(dag) > 0 {
		output: dag["\(len(dag)-1)"].output
	}
}


// A build step is anything that produces a docker image
#Step: {
	input?: docker.#Image
	output: docker.#Image
	...
}

// Build step that modifies an image by executing a command
#Run: {
	docker.#Run & {
		image: input
		export: rootfs: _
	}
	export: _

	input: docker.#Image
	output: docker.#Image & {
		rootfs: export.rootfs
		config: input.config
	}
}

// Build step that copies files into the container image
#Copy: {
	input:    docker.#Image
	contents: dagger.#FS
	source:   string | *"/"
	dest:     string | *"/"

	// Execute copy operation
	_copy: engine.#Copy & {
		"input": input.rootfs
		"source": {
			root: contents
			path: source
		}
		"dest": dest
	}

	output: docker.#Image & {
		config: input.config
		rootfs: _copy.output
	}
}

// Build step that executes a Dockerfile
#Dockerfile: {
	// Source directory
	source: dagger.#FS

	// FIXME: not yet implemented
	*{
		// Look for Dockerfile in source at default path
		path: "Dockerfile"
	} | {
		// Look for Dockerfile in source at a custom path
		path: string
	} | {
		// Custom dockerfile  contents
		contents: string
	}
}
