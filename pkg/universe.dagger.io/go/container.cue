// Go operation
package go

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/docker"
)

// A standalone go environment to run go command
#Container: {
	// Container app name
	name: *"go_builder" | string

	// Source code
	source: dagger.#FS

	// Use go image
	_image: #Image

	_sourcePath: "/src"
	_cachePath:  "/root/.cache/gocache"

	docker.#Run & {
		input:   *_image.output | docker.#Image
		workdir: "/src"
		command: name: "go"
		mounts: {
			"source": {
				dest:     _sourcePath
				contents: source
			}
			"go assets cache": {
				contents: core.#CacheDir & {
					id: "\(name)_assets"
				}
				dest: _cachePath
			}
		}
		env: GOMODCACHE: _cachePath
	}
}
