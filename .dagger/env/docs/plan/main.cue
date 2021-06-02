package main

import (
	"dagger.io/dagger"
	"dagger.io/js/yarn"
	"dagger.io/netlify"
)

// dagger repository
repository: dagger.#Artifact @dagger(input)

// Build the docs website
docs: yarn.#Package & {
	source:   repository
	cwd:      "tools/daggosaurus/"
	buildDir: "tools/daggosaurus/build"
}

// Deploy the docs website
site: netlify.#Site & {
	name:     string | *"docs-dagger-io" @dagger(input)
	contents: docs.build
}
