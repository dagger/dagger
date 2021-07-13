package plan

import (
	"alpha.dagger.io/netlify"
)

// Deploy the docs website
site: netlify.#Site & {
	name:     string | *"docs-dagger-io" @dagger(input)
}
