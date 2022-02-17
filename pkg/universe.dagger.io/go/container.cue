// Go operation
package go

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

// A standalone go environment to run go command
#Container: {
	// Go version to use
	version: *#DefaultVersion | string

	// Source code
	source: dagger.#FS

	// Configure caching
	cache: {
		id: *"go_build" | string
	}

	// Use go image
	_image: #Image & {
		"version": version
	}

	_sourcePath: "/src"
	_cachePath:  "/root/.cache/gocache"

	docker.#Run & {
		input:   _image.output
		workdir: "/src"
		command: name: "go"
		mounts: {
			"source": {
				dest:     _sourcePath
				contents: source
			}
			"go assets cache": {
				contents: dagger.#CacheDir & {
					id: "\(cache.id)_assets"
				}
				dest: _cachePath
			}
		}
		env: {
			CGO_ENABLED: "0"
			GOMODCACHE:  _cachePath
		}
	}
}
