package main

import "dagger.io/dagger/engine"

engine.#Plan & {
	actions: fetch: engine.#HTTPFetch & {
		source: "https://releases.dagger.io/dagger/latest_version"
		dest:   "/latest.html"
	}
}
