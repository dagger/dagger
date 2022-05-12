package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: {
		env: TMP_DIR_PATH: string
		filesystem: ref: read: {
			path:     env.TMP_DIR_PATH
			contents: dagger.#FS
		}
	}
	actions: test: {}
}
