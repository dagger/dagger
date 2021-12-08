package testing

import "alpha.dagger.io/dagger/op"

#up: [
	op.#FetchContainer & {
		ref: "busybox"
	},
	op.#Exec & {
		args: ["true"]
		dir: "/"
	},
]
