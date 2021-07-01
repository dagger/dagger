// OS operations
package os

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

// Built-in file implementation, using buildkit
// A single file
#File: {
	from: dagger.#Artifact | *[op.#Mkdir & {dir: "/", path: "/"}]
	path: string

	// Optionally write data to the file
	write: *null | {
		data: string
		// FIXME: append
		// FIXME: create + mode
	}

	// The contents of the file
	// If a write operation is specified, it is applied first.
	contents: {
		string

		#up: [
			op.#Load & {
				"from": from
			},
			if write != null {
				op.#WriteFile & {
					dest:    path
					content: write.data
				}
			},
			op.#Export & {
				source: path
				format: "string"
			},
		]
	}
}
