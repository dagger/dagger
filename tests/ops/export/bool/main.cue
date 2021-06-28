package testing

import "alpha.dagger.io/dagger/op"

TestExportBool: {
	bool

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				printf "true" > /tmp/out
				""",
			]
			dir: "/"
		},
		op.#Export & {
			// Source path in the container
			source: "/tmp/out"
			format: "json"
		},
	]
}
