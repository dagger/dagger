package testing

import (
	"dagger.io/dagger/op"
	"dagger.io/def"
)

#up: [
	op.#Load & {
		from: def
	},
]
