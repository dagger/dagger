# Zenith Examples

For now, environments are easiest to setup as subdirectories in the dagger repo. This is just due to the requirements to use development versions of SDKs, not a permanent feature.

For these examples, we'll create new environments in the dagger repo.

## Go Environment

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

- Dependencies can be relative local paths or git paths. See the later section "Referencing Environments in Git" for the `git://` format to use when specifying those

That command, if successful, will update `dagger.gen.go` with the new bindings for the environment you just extended with.

Additionally, if you ever need to update codegen bindings for any extensions, you can just run

```console
dagger env sync
```

to bring it up to date.

## Python Environment

After running `mkdir -p ./pythonexample && cd ./pythonexample`, you need to initialize your environment:

```console
dagger env init --name pythonexample --sdk python --root ..
```

- The `name` and `sdk` flags are self-explanatory
- The `root` flag is an ugly artifact of the need to use development SDKs and thus load the entire dagger repo, so it just needs to point to the root of the repo. This is just a temporary hack, not a permanent feature.

After that, you will see a `dagger.json` file in your current dir. That holds your environment configuration.

- Custom codegen for Python envs, similar to what's described for Go envs above, has not yet been implemented

To implement environment code, you need to create a `main.py` file and a `pyproject.toml` file in your current dir. See the `universe/_demo/client` dir for an example to help get started.

## Referencing Environments in Git

Environments can be specified as code in local paths or as references to code in a git repository.

For example, you can run this to execute the current demo:

```console
dagger checks -e 'git://github.com/sipsma/dagger?ref=zenith&subdir=universe/_demo'
```

Or you can execute this to add a dependency on the demo server environment:

```console
dagger env extend 'git://github.com/sipsma/dagger?ref=zenith&subdir=universe/_demo/server'
```

In general, the `git://` URL format is not something we're especially happy with and intend to improve, but the idea is:

- always prefaced with `git://<url of the repo>`
- everything else is an optional parameter, currently encoded w/ query params
  - `ref=<git ref>` lets you choose a branch, tag or commit from the repo. Defaults to `main`
  - `subdir=<path in the repo to dir containing dagger.json>` lets you select a subdir. Defaults to the root of the repo (`/`)
