package dagger

import (
	"encoding/yaml"
	"encoding/json"
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
	input: engine.#FS

	// Path of the subdirectory
	// Example: "/build"
	path: string

	// Copy action
	_copy: engine.#Copy & {
		"input":  engine.#Scratch
		contents: input
		source:   path
		dest:     "/"
	}

	// Subdirectory tree
	output: engine.#FS & _copy.output
}

// DecodeSecret is a convenience wrapper around #TransformSecret. The plain text contents of input is expected to match the format
#DecodeSecret: {
	{
		format: "json"
		engine.#TransformSecret & {
			#function: {
				input:  _
				output: json.Unmarshal(input)
			}
		}
	} | {
		format: "yaml"
		engine.#TransformSecret & {
			#function: {
				input:  _
				output: yaml.Unmarshal(input)
			}
		}
	}

}
