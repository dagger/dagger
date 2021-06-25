package testing

import "alpha.dagger.io/dagger/op"

#up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		args: ["echo", "simple output"]
	},
]
