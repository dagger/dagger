package main

import (
	"dagger.io/netlify"
	"dagger.io/js/react"
	"dagger.io/git"
)

siteID: string @dagger(input)

// Source code of the sample application
repo: git.#Repository & {
	remote: "https://github.com/kabirbaidhya/react-todo-app.git"
	ref:    "624041b17bd62292143f99bce474a0e3c2d2dd61"
}

// Host the application with Netlify
www: netlify.#Site & {

	// Site name can be overridden
	name: string | *"dagger-examples-react" @dagger(input)

	// Deploy the output of yarn build
	// (Netlify build feature is not used, to avoid extra cost).
	contents: app.build
}

app: react.#App & {
	source: repo
}
