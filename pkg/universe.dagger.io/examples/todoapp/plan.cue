package todoapp

import (
	"dagger.io/dagger"
	"universe.dagger.io/netlify"
	"universe.dagger.io/git"

	"universe.dagger.io/examples/todoapp/pkg/yarn"
)

dagger.#Plan & {
	client: {
		filesystem: {
			"./_build": write: contents: actions.build.output
			"./app/": write: contents:   actions.develop.output
			"./app": read: {
				contents: dagger.#FS
				exclude: [
					".git",
					"node_modules",
					"*.md",
				]
			}
		}
		env: {
			NETLIFY_TOKEN: dagger.#Secret
			APP_NAME:      string
		}
	}
	actions: {
		// Checkout a fresh copy of the todoapp repo
		develop: git.#Pull & {
			remote: "https://github.com/mdn/todo-react"
			ref:    "master"
		}

		yarn.#App & {
			source: client.filesystem."./app".read.contents
			name:   "todoapp"
		}

		// Deploy the application
		deploy: netlify.#Deploy & {
			contents: actions.build.output
			site:     client.env.APP_NAME
			token:    client.env.NETLIFY_TOKEN
		}
	}
}
