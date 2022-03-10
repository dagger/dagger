package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: {
		alpineBase: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}
		busyboxBase: dagger.#Pull & {
			source: "busybox:1.34.1@sha256:1286c6d3c393023ef93c247724a6a2d665528144ffe07bacb741cc2b4edfefad"
		}

		exec1: dagger.#Exec & {
			input: alpineBase.output
			args: [
				"sh", "-c",
				#"""
					mkdir /dir && echo -n foo > /dir/foo && echo -n removeme > /removeme
					"""#,
			]
		}

		exec2: dagger.#Exec & {
			input: exec1.output
			args: [
				"sh", "-c",
				#"""
					echo -n bar > /dir/bar && rm removeme
					"""#,
			]
		}

		removeme: dagger.#WriteFile & {
			input:    dagger.#Scratch
			path:     "/removeme"
			contents: "removeme"
		}

		test: {
			diff: dagger.#Diff & {
				lower: alpineBase.output
				upper: exec2.output
			}

			verify_diff_foo: dagger.#ReadFile & {
				input: diff.output
				path:  "/dir/foo"
			} & {
				contents: "foo"
			}
			verify_diff_bar: dagger.#ReadFile & {
				input: diff.output
				path:  "/dir/bar"
			} & {
				contents: "bar"
			}

			mergediff: dagger.#Merge & {
				inputs: [
					busyboxBase.output,
					removeme.output,
					diff.output,
				]
			}
			verify_remove: dagger.#Exec & {
				input: mergediff.output
				args: ["test", "!", "-e", "/removeme"]
			}
			verify_no_alpine_base: dagger.#Exec & {
				input: mergediff.output
				// make sure the the Diff actually separated files from the base
				// by testing the non-existence of a file that only exists in the 
				// alpine base, not busybox
				args: ["test", "!", "-e", "/etc/alpine-release"]
			}
		}
	}
}
