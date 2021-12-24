package dagger

import (
	"alpha.dagger.io/europa/dagger/engine"
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

	// Base image
	scratch: engine.#Scratch

	// Copy action
	copy: engine.#Copy & {
		"input": scratch.output
		source: {
			root:   input
			"path": path
		}
		dest: "/"
	}
}
