package testing

import "dagger.io/dagger/op"

#up: [
	op.#FetchContainer & {
		do:  "fetch-container"
		ref: "alpine"
	},
	op.#Exec & {
		args: ["sh", "-c", """
			[ "$foo" == "output environment" ] || exit 1
			"""]
		env: foo: "output environment"
	},
]
