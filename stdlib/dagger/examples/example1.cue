package main

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/llb2"
)

// Base Alpine filesystem
base: dagger.#Container & {
	fs: llb2.#DockerPull & {
		source: "alpine"
	}
	mount: {
		"/cache": source: llb2.#CacheDir
		"/netlify.key": source: token
	}
}

token: llb2.#Secret
