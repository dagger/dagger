// Deploy to Netlify
package todoapp

import (
	"universe.dagger.io/netlify"
)

// Netlify API token
input: secrets: netlify: _

// Must be a valid branch/PR name
environment: string

actions: {

	// Yarn build inherited from base config
	build: _

	deploy: netlify.#Deploy & {
		contents: build.output
		token:    input.secrets.netlify.contents
		site:     *"acme-inc-\(environment)" | string
		team:     *"acme-inc" | string
	}
}
