package main

import "dagger.io/dagger/engine"

engine.#Plan & {
	actions: fetch: engine.#HTTPFetch & {
		source: "https://releases.dagger.io/dagger/asfgdsfg"
		dest:   "/latest.html"
	}
}
