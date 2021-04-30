package io

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

#Dir: {
	from: dagger.#Artifact
	path: string

	// Read the tree structure (not file contents)
	read: tree: {
		string// FIXME: only raw 'find' output for now
		#up: [
			op.#FetchContainer & {
				ref: "alpine" // FIXME: use alpine or docker package
			},
			op.#Exec & {
				mount: "/data": "from": from
				args: [
					"sh", "-c",
					#"find /data | sed 's/^\/data//' > /export.txt"#,
				]
			},
			op.#Export & {
				source: "/export.txt"
				format: "string"
			},
		]
	}
}

#File: {
	from: dagger.#Artifact
	path: string
	read: {
		format: "string" | "json" | "yaml" | "lines"
		data: {
			string
			#up: [
				op.#Load & {"from":   from},
				op.#Export & {source: path, "format": format},
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
