package main

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/js/yarn"
	"alpha.dagger.io/netlify"
)

// dagger repository
repository: dagger.#Artifact @dagger(input)

// Build the docs website
docs: yarn.#Package & {
	source:   repository
	cwd:      "website/"
	buildDir: "website/build"
	env: {
		OAUTH_ENABLE:                   "true"
		REACT_APP_OAUTH_SCOPE:          "user:email"
		REACT_APP_GITHUB_AUTHORIZE_URI: "https://github.com/login/oauth/authorize?client_id=${REACT_APP_CLIENT_ID}&scope=${REACT_APP_OAUTH_SCOPE}&allow_signup=false"
		REACT_APP_DAGGER_SITE_URI:      "https://dagger.io"
		REACT_APP_API_PROXY_ENABLE:     "true"
	}
}

// Deploy the docs website
site: netlify.#Site & {
	name:     string | *"docs-dagger-io" @dagger(input)
	contents: docs.build
}
