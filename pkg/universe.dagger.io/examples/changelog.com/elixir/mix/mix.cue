package mix

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
)

#Get: #Run & {
	// Applies to all environments
	env: null
	cache: {
		build: null
		deps:  "locked"
	}
	container: command: {
		name: "sh"
		flags: "-c": "mix do deps.get"
	}
}

// Compile Elixir dependencies, including the app
#Compile: #Run & {
	cache: {
		build: "locked"
		deps:  "locked"
	}
	container: command: {
		name: "sh"
		flags: "-c": "mix do deps.compile, compile"
	}
}

// Run mix task with all necessary mounts so compiled artefacts get cached
// FIXME: add default image to hexpm/elixir:1.13.2-erlang-23.3.4.11-debian-bullseye-20210902
#Run: {
	app: {
		// Application name
		name: string

		// Application source code
		source: dagger.#FS
	}

	// Mix environment
	env: string | null

	// Configure mix caching
	// FIXME: simpler interface, eg. "ro" | "rw"
	cache: {
		// Dependencies cache
		deps: null | "locked"

		// Build cache
		build: null | "locked"
	}

	// Run mix in a docker container
	container: docker.#Run & {
		if env != null {
			"env": MIX_ENV: env
		}
		workdir: mounts.app.dest
		mounts: "app": {
			contents: app.source
			dest:     "/mix/app"
		}
		if cache.deps != null {
			mounts: deps: {
				contents: core.#CacheDir & {
					id:          "\(app.name)_deps"
					concurrency: cache.deps
				}
				dest: "\(mounts.app.dest)/deps"
			}
		}
		if cache.build != null {
			mounts: buildCache: {
				contents: core.#CacheDir & {
					id:          "\(app.name)_build_\(env)"
					concurrency: cache.build
				}
				dest: "\(mounts.app.dest)/_build/\(env)"
			}
		}
	}
}
