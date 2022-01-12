package dagger

import (
	"dagger.io/dagger/engine"
)

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
