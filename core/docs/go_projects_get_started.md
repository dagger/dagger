# What is a Project?

# Setup
TODO <DOWNLOAD LATEST CLI>

# Go
## Creating a New Project

TODO
`dagger project init --name my-project --sdk go`
`dagger do --help`
`dagger do say-hello --help`
`dagger do say-hello --to Daggernaut`
`dagger do test --pkg-path ./path/to/pkg`
`dagger do build --pkg-path ./cmd/foo`
`dagger do --output /tmp/build-output build --pkg-path ./cmd/foo`

`dagger project init -p ci/ --name my-project --sdk go --root ..`
<SAME COMMANDS AS ABOVE BUT WITH -p>

## Adding Commands to Your Project
Adding a command can be as simple as writing a single `func`. There are a few restrictions on the type signature of the func at this time:
* The first arg must be `ctx dagger.Context`
* You can add as many other args as you want, but they currently must be of type `string`
* Return types can be
  * `(string, error)`
  * `(*dagger.Directory, error)`
  * `(*dagger.File, error)`

Any function you write just needs to be provided as an arg to `dagger.ServeCommands` in the `main` func and it will show up as a command in `dagger do`.

### Execution Environment
Your command code executes inside a container in the Dagger engine, so it doesn't have direct access to files and environment variables available on the host. This is beneficial because:
1. The `dagger do` caller doesn't need language-specific toolchains installed at the right versions in order to execute the code. That's all taken care of by the containerized environment.
1. It's safer, especially if running not-fully-trusted commands.

However, sometimes you do need to access select resources from the caller's host. Currently, this is possible in a few limited ways:
1. The project's root directory is imported into the engine and will be the workdir of the command code. If you need to load any source files from your project, you can call use the `ctx.Client().Host()` APIs from your command code.
2. If environment variables or files outside the project root are needed, currently the only option is to pass them as a string to the command.
   * E.g. if your command is `Foo(ctx dagger.Context, bar string) (string, error)` then you callers can provide:
     * An env var like `dagger do foo --bar "$BARVAL"`
     * A file outside the project root `dagger do foo --bar "$(cat /some/arbitrary/file)"`
   * We may add more sugar to make this more seamless in the future.

You have a dagger client to use at `ctx.Client()` for all your Dagger needs. You also can write arbitrary code too though. You can import libraries and do anything you'd normally do in Go.

Every execution of each command is cached in the exact same way Dagger caches any `Exec`. If you run the same command with the same inputs, execution will be skipped and the cached result will be returned instead. This includes both your calls with the Dagger client and any arbitrary code you write too.

### Directory and File Outputs
If a command returns a `*dagger.File` or `*dagger.Directory`, then the default behavior is to overlay the file/dir to the project's root dir on the `dagger do` caller's host.

You can also provide an `--output` argument to `dagger do` to override the path where these will be written.

### Command Hierarchies
TODO

## Remote Projects
TODO
