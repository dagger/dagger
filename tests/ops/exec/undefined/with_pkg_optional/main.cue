package testing

import (
	"dagger.io/optional"
	"dagger.io/dagger/op"
)

#up: [
	op.#Load & {
		from: optional
	},
]
