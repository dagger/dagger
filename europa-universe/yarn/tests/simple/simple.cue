package yarn

import (
	"dagger.io/dagger/engine"
)

b: #Build & {
	source: engine.#Scratch
}

out: b.output
