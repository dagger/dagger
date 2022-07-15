package main

import (
	"encoding/yaml"
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: test: {
		write: core.#WriteFile & {
			input:    dagger.#Scratch
			path:     "/secret"
			contents: yaml.Marshal({
				FOO: "bar"
			})
		}

		secret: core.#NewSecret & {
			input: write.output
			path:  "/secret"
		}

		decode: core.#DecodeSecret & {
			input:  secret.output
			format: "yaml"
		}

		image: core.#Pull & {
			source: "alpine"
		}

		typo: core.#Exec & {
			input: image.output
			env: FOO: decode.output.FOOT.contents
			args: ["env"]
		}
	}
}
