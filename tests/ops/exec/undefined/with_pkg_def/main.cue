package testing

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/def"
)

#up: [
	op.#Load & {
		from: def
	},
]
