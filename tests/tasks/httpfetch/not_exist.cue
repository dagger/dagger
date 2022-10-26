package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: fetch: core.#HTTPFetch & {
		source: "https://releases.dagger.io/dagger/asfgdsfg"
		dest:   "/latest.html"
	}
}
