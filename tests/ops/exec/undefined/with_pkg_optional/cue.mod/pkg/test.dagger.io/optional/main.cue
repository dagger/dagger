package optional

import "alpha.dagger.io/dagger/op"

dang?: string

#up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		args: ["sh", "-c", """
			echo success
			"""]
	},
]
