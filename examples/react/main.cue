package main

import (
	"dagger.io/netlify"
	"dagger.io/yarn"
	"dagger.io/git"
)

// Source code of the sample application
repo: git.#Repository & {
	remote: "https://github.com/kabirbaidhya/react-todo-app.git"
	ref:    "624041b17bd62292143f99bce474a0e3c2d2dd61"
}

// Host the application with Netlify
www: netlify.#Site & {
	// Site name can be overridden
	name: string | *"dagger-example-react"

	// Deploy the output of yarn build
	// (Netlify build feature is not used, to avoid extra cost).
	contents: build
}

// Build the application with Yarn
build: yarn.#Script & {
	// What to build
	source: repo

	// How to build it (name of yarn script)
	run: "build"
}
