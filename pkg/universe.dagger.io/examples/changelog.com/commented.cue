package changelog

actions: {
	// Reuse in all mix commands

	// prod: assets: docker.#Build & {
	//  steps: [
	//   // 1. Start from dev assets :)
	//   dev.assets,
	//   // 2. Mix magical command
	//   mix.#Run & {
	//    script: "mix phx.digest"
	//    mix: {
	//     env:        "prod"
	//     app:        _appName
	//     depsCache:  "private"
	//     buildCache: "private"
	//    }
	//    workdir: _
	//    // FIXME: remove copy-pasta
	//    mounts: nodeModules: {
	//     contents: core.#CacheDir & {
	//      // FIXME: do we need an ID here?
	//      id: "\(mix.app)_assets_node_modules"
	//      // FIXME: does this command need write access to node_modules cache?
	//      concurrency: "private"
	//     }
	//     dest: "\(workdir)/node_modules"
	//    }
	//   },
	//  ]
	// }

	// dev: {
	// compile: mix.#Compile & {
	//  env:    "dev"
	//  app:    "thechangelog"
	//  base:   inputs.params.runtimeImage
	//  source: inputs.directories.app.contents
	// }

	// assets: docker.#Build & {
	//  steps: [
	//   // 1. Start from dev runtime build
	//   {
	//    output: build.output
	//   },
	//   // 2. Build web assets
	//   mix.#Run & {
	//    mix: {
	//     env:        "dev"
	//     app:        _appName
	//     depsCache:  "private"
	//     buildCache: "private"
	//    }
	//    // FIXME: move this to a reusable def (yarn package? or private?)
	//    mounts: nodeModules: {
	//     contents: core.#CacheDir & {
	//      // FIXME: do we need an ID here?
	//      id: "\(mix.app)_assets_node_modules"
	//      // FIXME: will there be multiple writers?
	//      concurrency: "locked"
	//     }
	//     dest: "\(workdir)/node_modules"
	//    }
	//    // FIXME: run 'yarn install' and 'yarn run compile' separately, with different caching?
	//    // FIXME: can we reuse universe.dagger.io/yarn ???? 0:-)
	//    script:  "yarn install --frozen-lockfile && yarn run compile"
	//    workdir: "/app/assets"
	//   },
	//  ]
	// }
	// }
	// test: {
	//  build: mix.#Build & {
	//   env:    "test"
	//   app:    _appName
	//   base:   inputs.params.runtimeImage
	//   source: inputs.directories.app.contents
	//  }

	//  // Run tests
	//  run: docker.#Run & {
	//   image:  build.output
	//   script: "mix test"
	//  }

	//  db: {
	//   // Pull test DB image
	//   pull: docker.#Pull & {
	//    source: inputs.params.test_db_image
	//   }

	//   // Run test DB
	//   // FIXME: kill once no longer needed (when tests are done running)
	//   run: docker.#Run & {
	//    image: pull.output
	//   }
	//  }
	// }
}
