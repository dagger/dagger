## v2 refs

A ref is a short URI-like string that refers to a versioned and/or
content-addressed object, such as a file or a directory stored in a Git
repository or artifacts in an OCI registry.

## Format

The following diagram summarizes the format with `[brackets]` denoting optional
components:

              authority
             ┌────┴────┐
    [git:][//example.org/]acme/repo[//subdir/foo][:v0.10.1][@d44c734db]
     └┬─┘  └──────────┬───────────┘ └─────┬────┘   └──┬──┘   └───┬───┘
    scheme          source             subpath       tag       digest

* `git:` is an optional URI scheme. See [Proposed schemes](#proposed-schemes)
  for more information.

* `//example.org/acme/repo` identifies the source, i.e. the object to be
  fetched.

* `//subdir/foo` is a subpath within the fetched source, using double-slash
  (`//`) to distinguish it from the source path.

* `:v0.10.1` is an optional tag. The subpath is prepended to semver tags, so
  this actually corresponds to the tag `subdir/foo/v1.2.3`.

* `@d44c734db` is an optional digest of the referenced content. For a Git ref
  this is a commit hash. For other schemes this may be a `sha256:...` digest.

The URI scheme determines the interpretation of the rest of the ref, but every
ref always has the same components with the same delimiters (`:`, `//`, `:`,
and `@`).

### Backwards compatibility

For backwards compatibility with module refs used prior to Zenith's launch, a
ref whose source refers to `github.com` will assume that the first two path
segments are `user/repo` and the rest of the path is actually the subpath.

That is, the following are equivalent:

```
github.com/a/b/c
github.com/a/b//c
git://github.com/a/b//c
```

## Goals

* Can refer to any versioned resource, but typically files or directories.

* Can specify a directory within the fetched (and possibly unpacked) directory.

* Docker-style semver shorthand (v1.2 means v1.2.0 >= x < v1.3.0).

* Semver with monorepos; sub-directory becomes tag prefix, e.g. `sdk/go/v1.2.3`

* Use schemes for syntax sugar (with a high threshold for entry).

* Can be used for both module refs and CLI flags.

## Non-goals

* Refs are platform-agnostic and refer to sources, not binaries. It is a
  non-goal to treat a link to a specific tarball like
  `dagger_v0.9.3_linux_amd64.tar.gz` like a ref.

## Constraints

* Avoid the characters `#`, `?`, and `&` as they have special meaning in shells
  and URLs, two places where these refs will be used all the time.

* Not locked in to any particular forge (e.g. `github.com`) (though it's OK to
  have shorthand).

## Proposed schemes

We might not need all these, just throwing ideas around:

* no scheme
    * A ref's scheme may be omitted, in which case it will be inferred based on
      whether the ref's source appears to refer to a remote authority.
    * For a source like `github.com/...` the remote default scheme will be
      used, e.g. `git:` or `dv:`.
    * For a source like `foo` the local default scheme will be used, e.g.
      `file:`, though it would also make sense to default this to something
      like `oci:` for image refs.
    * `.`, `..`, `./foo`, `../foo`, `/foo`, and `foo` are all interpreted as
      local refs.

* `file://foo/bar/baz`
    * refers to a path on the local filesystem

* `git://github.com/foo/bar//baz`
    * refers to the `/baz` subdirectory of the `https://github.com/foo/bar`
      repository

* `gh:vito/daggerverse/apko`
    * shorthand for `git://github.com/vito/daggerverse//apko`
    * `/apko` is inferred as subpath as all GitHub repos are `owner/repo`

* `gh://git.acme.com/vito/daggerverse/apko`
    * shorthand for `git://git.acme.com/vito/daggerverse//apko`
    * `//git.acme.com` indicates alternative GitHub host
    * `/apko` is inferred as subpath as all GitHub repos are `owner/repo`

* `dv:vito//apko`
    * shorthand for `git://github.com/vito/daggerverse//apko`

* `dv:aweris/gale//gale`
    * shorthand for `git://github.com/aweris/gale//daggerverse/gale`

* `dv://git.acme.com/vito//apko`
    * shorthand for `git://git.acme.com/vito/daggerverse//apko`

* `dv://gitlab.com/group-1/group-2/group-3//apko`
    * shorthand for `git://gitlab.com/group-1/group-2/group-3/daggerverse//apko`

### Possible future schemes

* `https://github.com/foo/bar//baz`
    * same as `git://`
    * will clone `https://github.com/foo/bar` and consume the `./baz` subdirectory
    * It was tempting to have this represent a plain old HTTP download, but in
      practice those URLs tend to bake in the version and even a platform,
      which goes against the spirit of refs.
    * Since it's not obvious whether we'll need this, I propose we just skip it
      for now.

* `oci:alpine`, `oci://docker.io/vito/foo`
    * An OCI registry ref
    * Tags and digests work the same as with conventional registry refs
    * Unclear what exactly we'll want to reference here, don't seem to need it
      yet, but the door is open
    * Maybe we transition `container.from` to a ref in a backwards-compatible way?

## Tagged refs

A ref's tag helps a human identify and control the version of the source being
referenced. While the digest component ultimately determines the content you
receive, the tag component is what originally discovers the digest, or allows a
human to recognize where the digest comes from after the fact.

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

A ref is _pinned_ when it contains a digest component. Otherwise the ref is
considered _moving_ - even if it contains a tag component.

To support reproducible builds, a moving ref should always be pinned prior to
being stored anywhere long-term (like `dagger.json`).

The pinning process involves expanding the tag to an exact version (e.g. `v1`
=> `v1.2.3`) and resolving the tag to a digest.

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
refs to be equivalent to any refs with the same source, subpath, and digest:

```
github.com/dagger/dagger@d44c734dbbbcecc75507003c07acabb16375891d
github.com/dagger/dagger:main@d44c734dbbbcecc75507003c07acabb16375891d
github.com/dagger/dagger:v0.9.3@d44c734dbbbcecc75507003c07acabb16375891d
```

## Crawling dependencies

Let's say the module at `dv:vito//testcontainers:v1.2.3@deadbeef` has a local
`../docker` ref as a dependency in its `dagger.json`:

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

1. Drop the tag portion from the ref, leaving only the digest.
    * An error must be raised if a digest is not present.
1. Combine the path with the origin ref's subpath.
    * An error must be raised if the result is outside of the source directory,
      i.e. if it begins with `../`.

Example:

```
dv:vito//testcontainers:v1.2.3@deadbeef
+ ../docker
= dv:vito//docker@deadbeef
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
