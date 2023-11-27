package multibucket

import (
	"alpha.dagger.io/netlify"
)

// Netlify site
site: "netlify": netlify.#Site & {
	contents: app.build
}
