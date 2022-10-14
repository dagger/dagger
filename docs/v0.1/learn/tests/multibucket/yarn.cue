package multibucket

import (
	"alpha.dagger.io/js/yarn"
)

// Build the source code using Yarn
app: yarn.#Package & {
	source: src
}
