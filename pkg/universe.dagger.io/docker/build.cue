package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
)

// Modular build API for Docker containers
#Build: {
	steps: [#Step, ...#Step]
	output: #Image

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
	input?: #Image
	output: #Image
	...
} | #Run

// Build step that copies files into the container image
#Copy: {
	input:    #Image
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

	output: #Image & {
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
