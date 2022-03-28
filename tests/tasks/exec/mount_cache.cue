package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: {
		image: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		sharedCache: core.#CacheDir & {
			id: "mycache"
		}

		exec: core.#Exec & {
			input: image.output
			mounts: cache: {
				dest:     "/cache"
				contents: sharedCache
			}
			args: [
				"sh", "-c",
				#"""
					echo -n hello world > /cache/output.txt
					"""#,
			]
		}

		test: {
			verify: core.#Exec & {
				input: image.output
				mounts: cache: {
					dest:     "/cache"
					contents: exec.mounts.cache.contents
				}
				args: [
					"sh", "-c",
					#"""
						test -f /cache/output.txt
						test "$(cat /cache/output.txt)" = "hello world"
						"""#,
				]
			}

			otherCache: core.#CacheDir & {
				id: "othercache"
			}
			verifyOtherCache: core.#Exec & {
				input: image.output
				mounts: cache: {
					dest:     "/cache"
					contents: otherCache
				}
				args: [
					"sh", "-c",
					#"""
						test ! -f /cache/output.txt
						"""#,
				]
			}
		}
	}
}
