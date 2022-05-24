package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: env: {
		DAGGER_CACHE_FROM?: string
		DAGGER_CACHE_TO?:   string
	}

	cache: (dagger.#FromCacheEnv & {
		imports: client.env.DAGGER_CACHE_FROM
		export:  client.env.DAGGER_CACHE_TO
	}).output

	actions: {
		image: core.#Pull & {
			source: "alpine:3.15.0"
		}

		exec: core.#Exec & {
			input: image.output
			args: [
				"sh", "-c",
				#"""
					echo -n hello world > /output.txt
					"""#,
			]
		}

		dir: core.#Mkdir & {
			input: dagger.#Scratch
			path:  "/dir"
		}

		dirfoo: core.#WriteFile & {
			input:    dir.output
			path:     "/dir/foo"
			contents: "foo"
		}

		dirfoo2: core.#WriteFile & {
			input:    dir.output
			path:     "/dir/foo"
			contents: "foo2"
		}

		dirbar: core.#WriteFile & {
			input:    dir.output
			path:     "/dir/bar"
			contents: "bar"
		}

		test: {
			merge: core.#Merge & {
				inputs: [
					dir.output,
					dirfoo.output,
					dirbar.output,
					exec.output,
					dirfoo2.output,
				]
			}

			verify_merge_output: core.#ReadFile & {
				input: merge.output
				path:  "/output.txt"
			} & {
				contents: "hello world"
			}
			verify_merge_dirbar: core.#ReadFile & {
				input: merge.output
				path:  "/dir/bar"
			} & {
				contents: "bar"
			}
			verify_merge_dirfoo: core.#ReadFile & {
				input: merge.output
				path:  "/dir/foo"
			} & {
				contents: "foo2"
			}
		}
	}
}
