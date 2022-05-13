package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: filesystem: {
		md: read: {
			path:     "."
			contents: dagger.#FS
			include: ["*.md"]
		}
		rst: read: {
			path:     "."
			contents: dagger.#FS
			include: ["*.rst"]
		}
	}
	actions: {
		_image: core.#Pull & {
			source: "alpine:3.15"
		}
		test: {
			md: core.#Exec & {
				input: _image.output
				mounts: opt: {
					contents: client.filesystem.md.read.contents
					dest:     "/opt"
				}
				args: ["sh", "-c", "test $(ls -1 /opt | wc -l) = 3"]
			}
			rst: core.#Exec & {
				input: _image.output
				mounts: opt: {
					contents: client.filesystem.rst.read.contents
					dest:     "/opt"
				}
				args: ["sh", "-c", "test $(ls -1 /opt | wc -l) = 2"]
			}
		}
	}
}
