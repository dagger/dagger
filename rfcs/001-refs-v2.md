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

## Tagged refs

A ref's tag helps a human identify and control the version of the source being
referenced. While the hash component ultimately determines the content you
receive, the tag component is what originally determines the hash.

When a tag is omitted the source is queried to find the latest version. If no
versions are found, the source is queried to determine a default tag. For a Git
source latest version is found by listing `refs/tags/v*`, and if no version is
found the default branch is used.

### Semantic versioning

Refs are designed to reference [semantically versioned](https://semver.org)
reproducible data, so while a tag may be any term understood by the source,
special meaning is given to tags that are valid versions.

#### Shorthand

When a tag is semver shorthand like `v1` or `v1.2` it is treated as a
constraint and expanded to a full version as part of pinning.

* `v1` means `v1.0.0 <= x < v2.0.0`
* `v1.2` means `v1.2.0 <= x < v1.3.0`

This mimicks conventions from Docker images without requiring authors to
publish and micromanage a bunch of extra tags for intermediate versions.

#### Scoping

When a ref has both a subpath and a semver tag the version is scoped to the
subpath.

* `github.com/dagger/dagger:v0.9.3` means `refs/tags/v0.9.3`
* `github.com/dagger/dagger//sdk/go:v0.9.3` means `refs/tags/sdk/go/v0.9.3`

This is a crucial element of supporting Git monorepos.

## Pinned refs

A ref is _pinned_ when it contains a hash component. Otherwise the ref is
considered _moving_ - even if it contains a tag component.

To support reproducible builds, a moving ref should always be pinned prior to
being stored anywhere long-term (like `dagger.json`).

The pinning process involves expanding the tag to an exact version (e.g. `v1`
=> `v1.2.3`) and resolving the tag to a hash.

Assuming the latest version of Dagger is currently v0.9.3, the following moving
refs all expand to the same pinned ref:

```
github.com/dagger/dagger
=> github.com/dagger/dagger:v0.9.3@d44c734dbbbcecc75507003c07acabb16375891d

github.com/dagger/dagger:v0
=> github.com/dagger/dagger:v0.9.3@d44c734dbbbcecc75507003c07acabb16375891d

github.com/dagger/dagger:v0.9
=> github.com/dagger/dagger:v0.9.3@d44c734dbbbcecc75507003c07acabb16375891d

github.com/dagger/dagger:v0.9.3
=> github.com/dagger/dagger:v0.9.3@d44c734dbbbcecc75507003c07acabb16375891d
```

When it comes to data deduplication, you can also consider all of the above
refs to be equivalent to any refs with the same source, subpath, and hash:

```
github.com/dagger/dagger@d44c734dbbbcecc75507003c07acabb16375891d
github.com/dagger/dagger:main@d44c734dbbbcecc75507003c07acabb16375891d
github.com/dagger/dagger:v0.9.3@d44c734dbbbcecc75507003c07acabb16375891d
```

## Crawling dependencies

Let's say the module at `mod://vito//testcontainers:v1.2.3@deadbeef` has a
local `../docker` ref as a dependency in its `dagger.json`:

```json
{
    "root": "..",
    "name": "testcontainers",
    "dependencies": [
        "../docker"
    ]
}
```

To crawl `../docker`, apply two steps to the origin ref:

1. Drop the tag portion from the ref, leaving only the hash.
  * An error must be raised if a hash is not present.
1. Combine the path with the origin ref's subpath.
  * An error must be raised if the result is outside of the source directory,
    i.e. if it begins with `../`.

Example:

```
mod://vito//testcontainers:v1.2.3@deadbeef
+ ../docker
= mod://vito//docker@deadbeef
```

Note that the `:v1.2.3` tag is dropped in the process. This is a mandatory step
because otherwise semver tags can become ambiguous.

Take the following example:

```
github.com/vito/my-mod:v1.2.3@deadbeef
+ ./submod/
= github.com/vito/mymod//submod:v1.2.3@deadbeef # WRONG
```

The ref above is trying to reference `refs/tags/v1.2.3`, but it appears to
reference `refs/tags/submod/v1.2.3` instead.
