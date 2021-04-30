package io

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

#File: {
	from: dagger.#Artifact
	path: string
	read: *null | {
		format: op.#Export.format
		data: {
			_
			#up: [
				op.#Load & {
					"from": from
				},
				op.#Export & {
					source:   path
					"format": format
				},
			]
		}
	}
	write: *null | {
		// FIXME: support writing in multiple formats
		// FIXME: append
		data: _
		#up: [
			op.#Load & {"from": from},
			op.#WriteFile & {
				dest:     path
				contents: data
			},
		]
	}
}
