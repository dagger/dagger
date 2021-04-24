package testing

import "dagger.io/dagger/op"

TestExportStringValidation: {
	string
	=~"^some.+"

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
