package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: fetch: dagger.#HTTPFetch & {
		source: "https://releases.dagger.io/dagger/latest_version"
		dest:   "/latest.html"
	}
}
