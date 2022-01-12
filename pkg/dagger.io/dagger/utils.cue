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

	// Subdirectory tree
	output: #FS & copy.output

	// Copy action
	copy: engine.#Copy & {
		"input": engine.#Scratch
		source: {
			root:   input
			"path": path
		}
		dest: "/"
	}
}
