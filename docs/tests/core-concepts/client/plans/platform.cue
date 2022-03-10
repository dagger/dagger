dagger.#Plan & {
	client: _

	actions: build: go.#Build & {
		os:   client.platform.os
		arch: client.platform.arch
		// ...
	}
}
