// Go operation
package go

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/docker"
)

// A standalone go environment to run go command
#Container: {
	// Go version to use
	version: *#Image.version | string

	// Source code
	source: dagger.#FS

	// Arguments
	args: [...string]

	// Use go image
	_image: #Image & {
		"version": version
	}

	_sourcePath: "/src"
	_cachePath:  "/root/.cache/gocache"

	docker.#Run & {
		input:   _image.output
		workdir: "/src"
		command: {
			name:   "go"
			"args": args
		}
		mounts: {
			"source": {
				dest:     _sourcePath
				contents: source
			}
			"go assets cache": {
				contents: engine.#CacheDir & {
					id: "\(_cachePath)_assets"
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
