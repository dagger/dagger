package testing

import (
	"alpha.dagger.io/optional"
	"alpha.dagger.io/dagger/op"
)

#up: [
	op.#Load & {
		from: optional
	},
]
