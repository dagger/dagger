Dagger implements a Bash-like shell for executing queries.

In Dagger Shell, commands correspond to GraphQL field selections.

Field and argument names are always kebab-case.

```sh
foo-bar # fooBar
```

A field's REQUIRED arguments correspond to POSITIONAL command arguments.

Example:

```sh
git https://github.com/dagger/dagger.git
```

A field's OPTIONAL arguments are passed as FLAGS.

Example:

```sh
# git(url: "https://github.com/dagger/dagger.git", keepGitDir: true)
git https://github.com/dagger/dagger.git --keep-git-dir
```

The scope of available commands is initially bound to the `Query` type, and
changes as commands are piped to one another.

When you pipe commands together with |, it's like chaining multiple API calls one after another in GraphQL.

Example:

```sh
container | from alpine
# {
#   container {
#     from(name: "alpine") {
#       ...
#     }
#   }
# }
```

Use `$(SUB-SHELLS)` to pass objects between commands.

Example:

```sh
container | from alpine | with-directory ./src/ $(git https://github.com/dagger/dagger.git | head | tree) | with-exec ls | stdout
```

Use VARIABLES to assign the output of a command and use it in subsequent commands.

Example:

```sh
repo=$(git https://github.com/dagger/dagger.git | head | tree)
container | from alpine | with-directory ./src/ $repo | with-exec ls | stdout
```

To pass lists of values as an argument, comma-separate the values like
`foo,bar,baz`. Do not use JSON syntax.

As a special exception, when a function takes a single required argument of
type `[String!]!`, you can pass a comma-separated list of values without
parentheses:

```sh
container | from alpine | with-exec echo hey | stdout
# prints "hey\n"
```
