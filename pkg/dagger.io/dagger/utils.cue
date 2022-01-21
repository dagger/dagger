package dagger

import (
	"dagger.io/dagger/engine"
)

// Access the source directory for the current CUE package
// This may safely be called from any package
#Source: engine.#Source

// A (best effort) persistent cache dir
#CacheDir: engine.#CacheDir

// A temporary directory for command execution
#TempDir: engine.#TempDir

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
