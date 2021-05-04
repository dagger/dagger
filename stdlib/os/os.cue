package os

import (
	"dagger.io/io"

	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

#Dir: io.#Dir & {
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

	io.#Reader & {
		read: {
			format: _
			data: {
				_
				#up: [
					op.#Load & {"from":   from},
					op.#Export & {source: path, "format": format},
				]
			}
		}
	}

	io.#Writer & {
		write: *null | {
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
}
