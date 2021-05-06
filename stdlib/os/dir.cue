package os

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

#Dir: {
	from: dagger.#Artifact
	path: string | *"/"

	#up: [
		op.#Load & {"from": from},
		op.#Subdir & {
			dir: path
		},
	]
}

#ReadDir: {
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
