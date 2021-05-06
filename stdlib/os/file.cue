package os

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

// Built-in file implementation, using buildkit
#File: {
	from: dagger.#Artifact
	path: string

	read: {
		// FIXME: support different data schemas for different formats
		format: "string"
		data: {
			string
			#up: [
				op.#Load & {"from":   from},
				op.#Export & {source: path, "format": format},
			]
		}
	}

	write: *null | {
		// FIXME: support encoding in different formats
		data: string
		#up: [
			op.#Load & {"from": from},
			op.#WriteFile & {
				dest:     path
				contents: data
			},
		]
	}
}
