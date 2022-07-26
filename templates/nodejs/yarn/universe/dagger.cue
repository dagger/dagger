package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/yarn"
)

dagger.#Plan & {
	actions: {
		build: {
			// core.#Source lets you access a file system tree (dagger.#FS)
			// using a path at "." or deeper (e.g. "./foo" or "./foo/bar") with
			// optional include/exclude of specific files/directories/globs
			checkoutCode: core.#Source & {
				path: "."
			}
			// Runs a `yarn install` in a container with the source code
			install: yarn.#Install & {
				source: checkoutCode.output
			}
			// Runs a `yarn build` script in a container with the source code
			build: yarn.#Script & {
				source: checkoutCode.output
				name:   "build"
			}
			// Runs a `yarn test` script in a container with the source code
			test: yarn.#Script & {
				source: checkoutCode.output
				name:   "test"
			}
		}
	}
}
