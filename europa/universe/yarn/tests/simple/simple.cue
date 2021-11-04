package yarn

import (
	"dagger.io/dagger/engine"
)

b: #Build & {
	source: engine.#Scratch.output
}

out: b.output
