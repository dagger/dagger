package testing

import (
	"dagger.io/nonoptional"
	"dagger.io/dagger/op"
)

#up: [
	op.#Load & {
		from: nonoptional
	},
]
