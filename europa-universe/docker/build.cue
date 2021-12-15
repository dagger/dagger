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
			///// FIXME: this section is broken and in the middle of debug + rewrite
			//// // 1. image -> input
			//// if (step.input == _|_) && ((step.image & #Image) != _|_) {
			////  input: image
			//// }

			//// // 2. 
			//// if ((step.output & docker.#Image) == _|_) && ((step.output.rootfs & dagger.#FS) != _|_) {
			////  
			//// }

			//// // As a special case, wrap #Run into a valid step
			//// if step.run != _|_ {
			////  "\(idx)": {
			////   input: _
			////   run:   step & {
			////    image: input
			////    output: rootfs: _
			////   }
			////   output: {
			////    config: input.config
			////    rootfs: run.output.rootfs
			////   }
			////  }
			//// }

			//// // Otherwise, just use the step as is
			//// if step.run == _|_ {
			////  "\(idx)": {
			////   run: false
			////   step
			////  }
			//// }

			"\(idx)": step

			// Either way, connect input to previous output
			if idx > 0 {
				"\(idx)": input: dag["\(idx-1)"].output
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
	copy: engine.#Copy & {
		"input": input.rootfs
		"source": {
			root: contents
			path: source
		}
		dest: copy.dest
	}

	output: #Image & {
		config: input.config
		rootfs: copy.output
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
