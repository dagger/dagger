package testing

import "dagger.io/dagger/op"

TestExportInvalidValidation: {
	string
	=~"^NAAAA.+"

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				echo -n something > /tmp/out
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
