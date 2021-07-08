package testing

import (
	"test.dagger.io/nonoptional"
	"alpha.dagger.io/dagger/op"
)

#up: [
	op.#Load & {
		from: nonoptional
	},
]
