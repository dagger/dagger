package testing

import "alpha.dagger.io/dagger/op"

TestExportString: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				printf something > /tmp/out
				""",
			]
		},
		op.#Export & {
			// Source path in the container
			source: "/tmp/out"
			format: "string"
		},
	]
}
