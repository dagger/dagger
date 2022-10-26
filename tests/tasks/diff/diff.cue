package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: {
		alpineBase: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}
		busyboxBase: core.#Pull & {
			source: "busybox:1.34.1@sha256:1286c6d3c393023ef93c247724a6a2d665528144ffe07bacb741cc2b4edfefad"
		}

		exec1: core.#Exec & {
			input: alpineBase.output
			args: [
				"sh", "-c",
				#"""
					mkdir /dir && echo -n foo > /dir/foo && echo -n removeme > /removeme
					"""#,
			]
		}

		exec2: core.#Exec & {
			input: exec1.output
			args: [
				"sh", "-c",
				#"""
					echo -n bar > /dir/bar && rm removeme
					"""#,
			]
		}

		removeme: core.#WriteFile & {
			input:    dagger.#Scratch
			path:     "/removeme"
			contents: "removeme"
		}

		test: {
			diff: core.#Diff & {
				lower: alpineBase.output
				upper: exec2.output
			}

			verify_diff_foo: core.#ReadFile & {
				input: diff.output
				path:  "/dir/foo"
			} & {
				contents: "foo"
			}
			verify_diff_bar: core.#ReadFile & {
				input: diff.output
				path:  "/dir/bar"
			} & {
				contents: "bar"
			}

			mergediff: core.#Merge & {
				inputs: [
					busyboxBase.output,
					removeme.output,
					diff.output,
				]
			}
			verify_remove: core.#Exec & {
				input: mergediff.output
				args: ["test", "!", "-e", "/removeme"]
			}
			verify_no_alpine_base: core.#Exec & {
				input: mergediff.output
				// make sure the the Diff actually separated files from the base
				// by testing the non-existence of a file that only exists in the
				// alpine base, not busybox
				args: ["test", "!", "-e", "/etc/alpine-release"]
			}
		}
	}
}
