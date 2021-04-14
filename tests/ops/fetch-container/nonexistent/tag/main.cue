package testing

import "dagger.io/dagger/op"

#up: [
	op.#FetchContainer & {
		ref: "alpine:doesnotexist"
	},
]
