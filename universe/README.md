# Playing With Zenith
When running any `dagger commands`, you will need to either prefix commands with `./hack/dev ./bin/dagger` or use a direnv-like setup that accomplishes the same. TODO: put a direnv setup under `./hack`?

## Adding to universe
* Put a directory under `universe/` with a `dagger.json` (can be handwritten or created with `dagger environment init`)
  * Note that the `root` value should currently always point to the root of the dagger repo; this is because we currently have a hard requirement to use all the dev sdks, not official release sdks.
  * Also note that setting the `include/exclude` fields as seen in other existing universe envs is highly advised as it will save 10+ seconds per dev loop that's otherwise spent loading data into the engine.

* Universe envs are builtin into standard dagger clients, so they will be picked up in codegen with `./hack/make sdk:all:generate`
  * As a result of the above, universe entries are also loaded when the server starts up, which causes the server to fail if something doesn't compile. You may only be able to see the error in this case with `docker logs dagger-engine.dev`
  * This is extremely annoying, should be helped by lazy loading of envs, but in the meantime there's a hack escape hatch by prefixing an env under universe with `_` (e.g. `universe/_brokenenv`), in which case we've currently hardcoded a check to skip loading it.

## Custom Codegen
It's also possible to generate a custom client for envs outside of universe, only in Go so far.
* In `dagger.json` you can manually add e.g. `"dependencies": ["./depA", "./depB"]`. You can only point to other local dirs containing your dependency envs at this time (support for `git://` envs is easy to add once desired)
* After that, run `dagger codegen` from the dir containing `dagger.json` (or `dagger codegen --env ./path/to/env/dir` and you will get a file `dagger.gen.go` next to `dagger.json`.
* That file contains a func `DaggerClient()` which returns a client that embeds the standard dagger Go client plus bindings to all the dependency envs.
