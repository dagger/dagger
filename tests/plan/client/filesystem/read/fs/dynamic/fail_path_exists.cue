package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: {
		env: TMP_DIR_PATH: string
		filesystem: {
			// should fail even if default path exists
			"./": read: {
				path:     env.TMP_DIR_PATH
				contents: dagger.#FS
			}
		}
	}
	actions: test: {}
}
