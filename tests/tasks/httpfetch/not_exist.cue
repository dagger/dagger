package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: fetch: dagger.#HTTPFetch & {
		source: "https://releases.dagger.io/dagger/asfgdsfg"
		dest:   "/latest.html"
	}
}
