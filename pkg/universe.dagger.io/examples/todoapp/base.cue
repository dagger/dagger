// Deployment plan for Dagger's example todoapp
package todoapp

import (
	"dagger.io/dagger"

	"universe.dagger.io/git"
	"universe.dagger.io/yarn"
)

dagger.#Plan & {
	// Build the app with yarn
	actions: build: yarn.#Build

	// Wire up source code to build
	{
		input: directories: source: _
		actions: build: source:     input.directories.source.contents
	} | {
		actions: {
			pull: git.#Pull & {
				remote: "https://github.com/mdn/todo-react"
				ref:    "master"
			}
			build: source: pull.output
		}
	}
}
