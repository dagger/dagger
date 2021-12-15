package mix

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/docker"
)

// Build an Elixir application with Mix
#Build: {
	// Ref to base image
	// FIXME: spin out docker.#Build for max flexibility
	//   Perhaps implement as a custom docker.#Build step?
	base: docker.#Ref

	// App name (for cache scoping)
	app: string

	// Mix environment
	env: string

	// Application source code
	source: dagger.#FS

	docker.#Build & {
		steps: [
			// 1. Pull base image
			docker.#Pull & {
				source: base
			},
			// 2. Copy app source
			docker.#Copy & {
				contents: source
				dest:     "/app"
			},
			// 3. Download dependencies into deps cache
			#Run & {
				mix: {
					"env":     env
					"app":     app
					depsCache: "locked"
				}
				workdir: "/app"
				script:  "mix deps.get"
			},
			// 4. Build!
			// FIXME: step 5 is to add image data, see issue 1339
			#Run & {
				mix: {
					"env":      env
					"app":      app
					depsCache:  "readonly"
					buildCache: "locked"
				}
				workdir: "/app"
				script:  "mix do deps.compile, compile"
			},
		]
	}
}

// Run mix correctly in a container
#Run: {
	mix: {
		app:         string
		env:         string
		depsCache?:  "readonly" | "locked"
		buildCache?: "readonly" | "locked"
	}
	docker.#Run
	env: MIX_ENV: mix.env
	{
		mix: depsCache: string
		workdir: string
		mounts: depsCache: {
			contents: engine.#CacheDir & {
				id:          "\(mix.app)_deps"
				concurrency: mix.depsCache
			}
			dest: "\(workdir)/deps"
		}
	} | {}
	{
		mix: buildCache: string
		workdir: string
		mounts: buildCache: {
			contents: engine.#CacheDir & {
				id:          "\(mix.app)_deps"
				concurrency: mix.buildCache
			}
			dest: "\(workdir)/deps"
		}
	} | {}
}
