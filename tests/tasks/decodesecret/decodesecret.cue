package main

import (
	"encoding/json"
	"encoding/yaml"
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

let data = {
	FOO: "bar"
	ONE: TWO: "twelve"
}

dagger.#Plan & {
	actions: test: {
		format: "json" | *"yaml"

		_formats: {
			"json": json.Marshal(data)
			"yaml": yaml.Marshal(data)
		}

		write: core.#WriteFile & {
			input:    dagger.#Scratch
			path:     "/secret"
			contents: _formats[format]
		}

		secret: core.#NewSecret & {
			input: write.output
			path:  "/secret"
		}

		decode: core.#DecodeSecret & {
			input:    secret.output
			"format": format
		}

		image: core.#Pull & {
			source: "alpine"
		}

		verify: core.#Exec & {
			input: image.output

			mounts: {
				secret1: {
					type:     "secret"
					contents: decode.output.FOO.contents
					dest:     "/secret1"
				}

				secret2: {
					type:     "secret"
					contents: decode.output.ONE.TWO.contents
					dest:     "/secret2"
				}
			}

			args: ["sh", "-c", "test $(cat /secret1) = bar && test $(cat /secret2) = twelve"]
		}
	}
}
