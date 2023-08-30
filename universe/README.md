# Playing With Zenith

In order to run dagger with Zenith functionality, you will need to build a Dagger CLI off this branch and build a Dagger Engine off this branch.

To do that, just run this from the dagger repo root:

```console
./hack/dev
export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://dagger-engine.dev
export PATH=$(pwd)/bin:$PATH
```

## Creating an Environment

For now, environments are easiest to setup as subdirectories in the dagger repo. This is just due to the requirements to use development versions of SDKs, not a permanent feature.

For these examples, we'll create new environments in the dagger repo.

### Go Environment

After running `mkdir -p ./goexample && cd ./goexample`, you need to initialize your environment:

```console
dagger env init --name goexample --sdk go --root ..
```

- The `name` and `sdk` flags are self-explanatory
- The `root` flag is an ugly artifact of the need to use development SDKs and thus load the entire dagger repo, so it just needs to point to the root of the repo. This is just a temporary hack, not a permanent feature.

After that, you will see a `dagger.json` file and a `dagger.gen.go` file in your current dir. These hold your environment configuration and generated dagger client code, respectively.

To implement environment code, you just need to create a `main.go` file in this dir and register your checks/artifacts/commands/shells/functions.

- You can see some existing examples in `universe/_demo/main.go`, `universe/_demo/server/main.go`, `universe/go/main.go`, and `universe/apko/apko.go`

If at any point you want to add a dependency on another environment, you can run:

```console
dagger env extend <./path/to/other/env>
```

- Currently, dependencies must be relative paths to other environment directories on the local filesystem. Support for `git://` envs is easy to add once requested 🙂

That command, if successful, will update `dagger.gen.go` with the new bindings for the environment you just extended with.

Additionally, if you ever need to update codegen bindings for any extensions, you can just run

```console
dagger env sync
```

to bring it up to date.

### Python Environment

After running `mkdir -p ./pythonexample && cd ./pythonexample`, you need to initialize your environment:

```console
dagger env init --name pythonexample --sdk python --root ..
```

- The `name` and `sdk` flags are self-explanatory
- The `root` flag is an ugly artifact of the need to use development SDKs and thus load the entire dagger repo, so it just needs to point to the root of the repo. This is just a temporary hack, not a permanent feature.

After that, you will see a `dagger.json` file in your current dir. That holds your environment configuration.

- Custom codegen for Python envs, similar to what's described for Go envs above, has not yet been implemented

To implement environment code, you need to create a `main.py` file and a `pyproject.toml` file in your current dir. See the `universe/_demo/client` dir for an example to help get started.
