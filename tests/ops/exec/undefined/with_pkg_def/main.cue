package testing

import (
	"alpha.dagger.io/dagger/op"
	"test.dagger.io/def"
)

#up: [
	op.#Load & {
		from: def
	},
]
