package main

import "alpha.dagger.io/europa/dagger/engine"

engine.#Plan & {
	actions: fetch: engine.#HTTPFetch & {
		source: "https://releases.dagger.io/dagger/latest_version"
		dest:   "/latest.html"
	}
}
