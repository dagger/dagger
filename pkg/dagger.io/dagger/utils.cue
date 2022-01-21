package dagger

import (
	"dagger.io/dagger/engine"
)

// Access the source directory for the current CUE package
// This may safely be called from any package
#Source: engine.#Source

// Select a subdirectory from a filesystem tree
#Subdir: {
	// Input tree
	input: #FS

	// Path of the subdirectory
	// Example: "/build"
	path: string

	// Copy action
	_copy: engine.#Copy & {
		"input": engine.#Scratch
		source: {
			root:   input
			"path": path
		}
		dest: "/"
	}

	// Subdirectory tree
	output: #FS & _copy.output
}
