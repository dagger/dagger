# Dagger CLI

> This is currently a throwaway markdown file for collaborating in a PR. Could
> be turned into a doc but that's not the current priority.

## Things you can do

Unless otherwise specified, all commands would 

* run tests/checks
    * TODO decide between `test` or `check`
        * `test` is arguably more familiar and guessable, but not every check
          will be a test suite. It could also be a linter, or whatever the user
          wants to run.
        * Maybe we should just go with `test` anyway, favoring familiarity over
          technical correctness? Arguably non-test-sutie checks are just
          integration tests. :grin:
    * `dagger check` - run all checks
    * `dagger check engine` - run a named check
    * `dagger check engine -- -run Services` - run a named check, passing args
        * Needs a standard way to pass argv to check callbacks.
    * `dagger test` - alternative
    * `dagger test engine` - run a named check
    * `dagger test engine -- -run Services` - run a named check, passing args
        * Needs a standard way to pass argv to check callbacks.
* build artifacts
    * `dagger build`
* list available artifacts
    * `dagger artifacts`
* extend your environment with another environment
    * `dagger use go` - universe environment
    * `dagger use ./go/` - local environment
    * `dagger use github.com/vito/progrock@main` - git environment
    * `dagger use github.com/vito/progrock/ci@main` - git environment, `./ci` subdirectory
* SDK codegen
    * `dagger generate`
    * Generates SDK code for all environments in `dagger.json`.
    * **note:** It's tempting to let the user run `go generate ./...` in Dagger
      here so it's all one step, but the SDK codegen probably needs to work
      when your code can't compile, too.
        * Maybe it could do SDK codegen and then _try_ to run the user's hook?
