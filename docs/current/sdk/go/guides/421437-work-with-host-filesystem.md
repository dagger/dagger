---
slug: /421437/work-with-host-filesystem
displayed_sidebar: 'current'
---

# Work with the Host Filesystem

## Introduction

This guide explains how to work with the host filesystem using the Dagger Go SDK. You will learn how to:

- Set the working directory on the host
- List host directory entries with include/exclude filters
- Mount a host directory in a container
- Export a directory from a container to the host

## Requirements

This guide assumes that:

- You have a Go development environment with Go 1.15 or later. If not, [download and install Go](https://go.dev/doc/install).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have a Go module with the Dagger Go SDK installed. If not, [install the Dagger Go SDK](../371491-install.md).

## Set the host working directory

The easiest way to set the working directory on the host for your Go CI pipeline is at the time of client instantiation, with the client's `WithWorkdir()` option.

The following example shows how to set the host working directory:

```go file=../snippets/work-with-host-filesystem/set-workdir/main.go
```

## List directory contents

The `Host` type provides information about the host's execution environment. Its `Directory()` method accepts a path and returns a reference to the corresponding host directory as a `Directory` struct. There's also a shortcut `Workdir()` method, which returns a reference to the current working directory on the host. Entries in the directory can be obtained via the `Directory.Entries()` function.

The following example shows how to list the contents of the host working directory:

```go file=../snippets/work-with-host-filesystem/list-dir/main.go
```

## List directory contents with filters

It's possible to restrict a `Directory` to a subset of directory entries, by specifying a list of filename patterns to include or exclude.

The following example shows how to obtain a reference to the host working directory containing only `*.rar` files:

```go file=../snippets/work-with-host-filesystem/list-dir-include/main.go
```

The following example shows how to obtain a reference to the host working directory containing all files except `*.txt` files:

```go file=../snippets/work-with-host-filesystem/list-dir-exclude/main.go
```

The `Exclude` pattern overrides the `Include` pattern, but not vice-versa. The following example demonstrates by obtaining a reference to the host working directory containing all files except `*.rar` files:

```go file=../snippets/work-with-host-filesystem/list-dir-exclude-include/main.go
```

## Mount a host directory in a container

A common operation when working with containers is to mount a host directory to a path in the container and then perform operations on it. This can be done using the `Container.WithMountedDirectory()` method, which accepts the mount point in the container and the `Directory` to be mounted as arguments.

The following example shows how to mount a host directory in a container at the `/host` container path and then execute a command in the container referencing the mounted directory:

```go file=../snippets/work-with-host-filesystem/mount-dir/main.go
```

## Export a directory from a container to the host

A directory can be exported to a different path using the `Directory.Export()` method. The destination path is supplied to the method as an argument.

The following example creates a file in a container's `/tmp` directory and then exports the contents of that directory to the host's temporary directory:

```go file=../snippets/work-with-host-filesystem/export-dir/main.go
```

## Conclusion

This guide introduced you to the functions available in the Dagger Go SDK to work with the host filesystem. It provided explanations and code samples demonstrating how to set the host working directory, read directory contents (with and without pathname filters), mount a host directory in a container and export a directory from a container to the host.

Use the [SDK Reference](https://pkg.go.dev/dagger.io/dagger) to learn more about the Dagger Go SDK.
