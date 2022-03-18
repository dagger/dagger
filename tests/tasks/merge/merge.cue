package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: {
		image: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		exec: dagger.#Exec & {
			input: image.output
			args: [
				"sh", "-c",
				#"""
					echo -n hello world > /output.txt
					"""#,
			]
		}

		dir: dagger.#Mkdir & {
			input: dagger.#Scratch
			path:  "/dir"
		}

		dirfoo: dagger.#WriteFile & {
			input:    dir.output
			path:     "/dir/foo"
			contents: "foo"
		}

		dirfoo2: dagger.#WriteFile & {
			input:    dir.output
			path:     "/dir/foo"
			contents: "foo2"
		}

		dirbar: dagger.#WriteFile & {
			input:    dir.output
			path:     "/dir/bar"
			contents: "bar"
		}

		test: {
			merge: dagger.#Merge & {
				inputs: [
					dir.output,
					dirfoo.output,
					dirbar.output,
					exec.output,
					dirfoo2.output,
				]
			}

			verify_merge_output: dagger.#ReadFile & {
				input: merge.output
				path:  "/output.txt"
			} & {
				contents: "hello world"
			}
			verify_merge_dirbar: dagger.#ReadFile & {
				input: merge.output
				path:  "/dir/bar"
			} & {
				contents: "bar"
			}
			verify_merge_dirfoo: dagger.#ReadFile & {
				input: merge.output
				path:  "/dir/foo"
			} & {
				contents: "foo2"
			}
		}
	}
}
