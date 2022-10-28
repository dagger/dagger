This package is mostly extracted from Buildkit v0.10.5's [`session/filesync`][src].

The following changes are made:

* The filesync provider no longer takes a predefined list of named directories.
  Instead, names _are_ the directories. This has _huge_ security implications
  which we'll address somehow on our own, but it allows us to have a single
  long-lived gateway session and expose host dirs through the API without
  having to know them upfront.
* The protobuf type has been renamed from `moby.filesync.v1.BytesMessage` to
  `dagger.filesync.v1.BytesMessage` to avoid duplicate registration.

[src]: https://github.com/moby/buildkit/tree/v0.10.5/session/filesync
