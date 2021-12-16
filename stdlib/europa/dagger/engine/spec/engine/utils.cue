package engine

import (
	"alpha.dagger.io/europa/dagger/engine/spec/engine"
)

// Select a subdirectory from a filesystem tree
#Subdir: {
	// Input tree
	input: #FS

	// Path of the subdirectory
	// Example: "/build"
	path: string

	// Subdirectory tree
	output: #FS & _copy.output

	_copy: engine.#Copy & {
		"input": engine.#Scratch.output
		source: {
			root:   input
			"path": path
		}
	}
}
