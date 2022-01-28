package yarn

import (
	"dagger.io/dagger"
	"universe.dagger.io/yarn"
)

dagger.#Plan & {
	// Inherit from base
	inputs: directories: testdata: _

	actions: build: yarn.#Build & {
		source: inputs.directories.testdata.contents
		// FIXME: make 'cache' optional
		cache: dagger.#CacheDir & {
			id: "yarn cache"
		}
	}
}
