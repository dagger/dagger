# Dagger CLI

> This is currently a throwaway markdown file for collaborating in a PR. Could
> be turned into a doc but that's not the current priority.

## CLI bird's eye view

Here are all the commands for things you can do, with alternatives inline for
side-by-side comparison.

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
    * `dagger test` - alternative
    * `dagger test engine`
    * `dagger test engine -- -run Services`
* build artifacts
    * `dagger build`
* list available artifacts
    * `dagger artifacts`
* extend your environment with another environment
    * `dagger use go` - universe environment
    * `dagger use ./go/` - local environment
    * `dagger use github.com/vito/progrock@main` - git environment
    * `dagger use github.com/vito/progrock/ci@main` - git environment, `./ci` subdirectory
* list memoized queries
    * `dagger memos` - lists queries and associated tags
    * `dagger inputs`
* re-evaluate all memoized queries
    * `dagger refresh` - refresh all memos
    * `dagger refresh foo` - refresh memos with `foo` tag
    * `dagger bump` - alternative, more familiar/less cute
    * `dagger bump foo`
* generate SDK client code
    * `dagger codegen` - generate SDK code for all environments in `dagger.json`
    * `dagger refresh` - alternative, refreshes all memories _and_ regenerates code?

### Side topic: memoization, aka general-purpose pinning/bumping

> This probably deserves a separate RFC but so long as it's entangled with the
> CLI UX I'll just include it here to maintain low overhead.

Here's an attempt at modeling `.lock` file semantics by caching GraphQL query
results - in other words, by [memoizing] them.

[memoizing]: https://en.wikipedia.org/wiki/Memoization

> We may want to use more familiar terms like "dependency" and "bump", but I
> figured I'd err on the side of technical accuracy for clarity's sake.

By supporting query memoization we can pin/lock/cache anything we want. You
could cache the `stdout` of a command that you run to resolve a package list to
package versions, as part of a scheme to support reproducible builds:

```go
packages, err := cc.Container().
    Memoize([]string{"apko-packages"}).
    From("cgr.dev/chainguard/apko"). // memoizes buildkit source
    WithExec([]string{"apko", "show-packages", "/config.yml", "--format"}).
    Stdout(ctx)
if err != nil {
    panic(err)
}
```

We can mimic bumping dependencies by just re-evaluating queries, without having
to re-run code:

```sh
$ dagger refresh apko-packages
```

> Re-evaluating code to bump dependencies is spooky because your code might have
> side effects, or it might not be able to compile if you're in the middle of
> adapting to backwards-incompatible schema changes.

#### Implementation notes

The API could look something like this:

```graphql
extend type Query {
  # Memoize all subsequent leaf node fields.
  memoize(tags: [String!]): Query!
}

extend type Container {
  # Memoize all subsequent leaf node fields.
  memoize(tags: [String!]): Container!
}

# File extends memoize so that you can memoize stdout.
extend type Directory {
  # Memoize all subsequent leaf node fields.
  memoize(tags: [String!]): Directory!
}

# File extends memoize so that you can memoize id and/or stdout.
extend type File {
  # Memoize all subsequent leaf node fields.
  memoize(tags: [String!]): File!
}

# not implemented for Secret; its plaintext value should never be
```

Each type decides which fields can/can't be memoized. This way we can prevent
memoizing sensitive information (`Secret.plaintext`) or effect-ful queries
(`Container.export`) and even store results for intermediate queries
(`Container.from`).

* There are parallels here to chats we've had about caching of `dagger do`
  commands.

* Buildkit source pinning could work with this by having e.g. `Container.from`
  and `Git.branch` memoize by returning a `ContainerID` that embeds a source
  policy.

* **TODO:** we'll need some way to prevent meta-queries like `pipeline()` and
  `memoize()` from becoming part of the cache key. If we can't come up with a
  better idea, we could always just reserve the words and drop them from the
  query path during memoization.

* **TODO:** How can this work with environment-provided resolvers and types? I
  guess we need some way to decorate resolvers as @memoizable? (Back to the
  special comment syntax discussion.)
