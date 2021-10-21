package nonoptional

import "alpha.dagger.io/dagger/op"

dang: string

#up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		args: ["sh", "-c", """
			echo "This test SHOULD fail, because this SHOULD be executed"
			exit 1
			"""]
	},
]
