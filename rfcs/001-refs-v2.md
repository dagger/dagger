## v2 refs

A ref is a short string that refers to a versioned and/or content-addressed
object, such as a file or a directory stored in a Git repository, or artifacts
in an OCI registry.

    [git://]example.org/dagger/dagger[//sdk/go][:v0.9.3][@d44c734db]
     scheme          source            path      tag       hash 

* `git://` is an optional URI scheme, with `git://` as the default. See
  [Proposed schemes](#proposed-schemes) for more information.

* `github.com/dagger/dagger` identifies the source location.

* `//sdk/go` is a subpath within the fetched source, using double-slash (`//`)
  to distinguish it from the source path.

* `:v1.2.3` is an optional tag. The subpath is prepended to semver tags, so
  this actually corresponds to the tag `sdk/go/v1.2.3`.

* `@d44c734db` is an optional hash of the referenced content. For a Git ref this is
  a commit hash. For other schemes this may be a `sha256:...` digest.

The URI scheme determines the interpretation of the rest of the ref, but every
ref always has the same components with the same delimiters (`://`, `//`, `:`,
and `@`).

## Goals

* Can specify a directory within the fetched (and possibly unpacked) directory.

* Docker-style semver shorthand (v1.2 means v1.2.0 >= x < v1.3.0).

* Semver with monorepos; sub-directory becomes tag prefix, e.g. `sdk/go/v1.2.3`

* Use schemes for syntax sugar (with a high threshold for entry).

* Can refer to any semver'd URL-able resource, but typically files or directories.

* Can be used for both module refs and CLI flags.

## Constraints

* Avoid the characters `#`, `?`, and `&` as they have special meaning in shells
  and URLs, two places where these refs will be used all the time.

* Not locked in to any particular forge (e.g. `github.com`) (though it's OK to
  have shorthand).

## Proposed schemes

We might not need all these, just throwing ideas around:

* no scheme
    * `.`, `..`, `./foo`, `../foo`, `/foo` are interpreted as local refs
    * otherwise, defaults to `git://`

* `git://github.com/foo/bar//baz`
    * will clone `https://github.com/foo/bar` and consume the `./baz` subdirectory

* `https://github.com/foo/bar//baz`
    * TODO: this is either same as above or references a single HTTP file to download + unpack, unsure
    * TODO: unpacking should maybe be determined by scheme `zip://github.com/vito/daggerverse/archive/refs/heads/v1.2.3.zip//apko@sha256:...`

* `gh://vito/daggerverse//apko`
    * shorthand for `git://github.com/vito/daggerverse//apko`
    * Can use github.com HTTP downloads as an optimization if needed

* `mod://vito//apko`
    * shorthand for `git://github.com/vito/daggerverse//apko`
    * Can use github.com HTTP downloads as an optimization if needed

* `mod://git.acme.com/vito//apko`
    * shorthand for `git://git.acme.com/vito/daggerverse//apko`

* `mod://gitlab.com/dir1/dir2/dir3//apko`
    * shorthand for `git://gitlab.com/dir1/dir2/dir3//daggerverse//apko`

* `oci://alpine`
    * OCI registry (TODO artifacts? images? who knows!)

* `dag://vito/booklit`
    * shorthand for `git://github.com/vito/booklit//dag`
    * alternative to `mod://vito/booklit//dag`
    * `dagger -m dag://vito/booklit download binary` if we want to branch into
      a package distribution mechanism (would be slick to also `chmod +x`)
    * this would only be worth it if we imagine it being a common thing and/or
      are interested in making `dag/` a convention

## Combining refs

Let's say the module at `mod://vito//testcontainers` has a local ref dependency
in its `dagger.json`:

```json
{
    "root": "..",
    "name": "testcontainers",
    "dependencies": [
        "../docker"
    ]
}
```

Local refs like `../docker` are made relative to the origin ref.

When crawling `mod://vito//testcontainers` and recursing into its dependencies,
this local `../docker` ref is made relative to the origin ref to yield:

```
mod://vito//docker
```

The tricky part is the above ref might resolve to a version different from the
one we came from.

When crawling `mod://vito//testcontainers:v1.2.3` we have to be more careful.
The semver tag here is shorthand for the tag `testcontainers/v1.2.3`, so we
need to preserve that when swapping the subdirectory for `//docker`:

```
mod://vito//docker:testcontainers/v1.2.3
```

Note that in practice the crawling would typically be from a hashed ref, which
would just be preserved in both cases and provide more safety either way.
