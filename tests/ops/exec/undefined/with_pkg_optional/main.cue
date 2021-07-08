package testing

import (
	"test.dagger.io/optional"
	"alpha.dagger.io/dagger/op"
)

#up: [
	op.#Load & {
		from: optional
	},
]
