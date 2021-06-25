package testing

import "alpha.dagger.io/dagger/op"

TestExportInvalidFormat: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				echo something > /tmp/out
				""",
			]
		},
		op.#Export & {
			// Source path in the container
			source: "/tmp/out"
			format: "lalalalal"
		},
	]
}
