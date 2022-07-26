package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/go"
)

dagger.#Plan & {
	actions: {
		build: {
			getCode: core.#Source & {
				path: "./src"
			}
			test: go.#Test & {
				source:  getCode.output
				package: "./..."
			}

			goBuild: go.#Build & {
				source: getCode.output
			}
		}
	}
}
