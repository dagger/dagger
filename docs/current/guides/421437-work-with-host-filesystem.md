---
slug: /421437/work-with-host-filesystem
displayed_sidebar: 'current'
category: "guides"
tags: ["go", "python", "nodejs"]
authors: ["Alex Suraci", "Vikram Vaswani"]
date: "2022-11-01"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Work with the Host Filesystem

## Introduction

This guide explains how to work with the host filesystem using the Dagger SDKs. You will learn how to:

- Set the working directory on the host
- List host directory entries with include/exclude filters
- Mount a host directory in a container
- Export a directory from a container to the host

## Requirements

This guide assumes that:

- You have a Go, Python or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have a Dagger SDK installed for one of the above languages. If not, follow the installation instructions for the Dagger [Go](../sdk/go/371491-install.md), [Python](../sdk/python/866944-install.md) or [Node.js](../sdk/nodejs/835948-install.md) SDK.
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## List directory contents

The easiest way to set the working directory for the Dagger CI pipeline is at the time of client instantiation, as a client configuration option. By default, Dagger uses the current directory on the host as the working directory.

The following example shows how to list the contents of the working directory:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/work-with-host-filesystem/list-dir/main.go
```

The `Host` type provides information about the host's execution environment. Its `Directory()` method accepts a path and returns a reference to the corresponding host directory as a `Directory` struct. Entries in the directory can be obtained via the `Directory.Entries()` function.

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/work-with-host-filesystem/list-dir/index.mts
```

The `host` type provides information about the host's execution environment. Its `directory()` method accepts a path and returns a reference to the corresponding host directory as a `Directory` object. Entries in the directory can be obtained via the `directory.entries()` function.

</TabItem>
<TabItem value="Python">

```python file=./snippets/work-with-host-filesystem/list-dir/main.py
```

The `host` type provides information about the host's execution environment. Its `directory()` method accepts a path and returns a reference to the corresponding host directory as a `Directory` object. Entries in the directory can be obtained via the `directory.entries()` function.

</TabItem>
</Tabs>

When the Dagger pipeline code is in a sub-directory, it may be more useful to set the parent directory (the project's root directory) as the working directory. The following example revises the previous one, setting the parent directory as the working directory and listing its contents:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/work-with-host-filesystem/list-dir-parent/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/work-with-host-filesystem/list-dir-parent/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/work-with-host-filesystem/list-dir-parent/main.py
```

</TabItem>
</Tabs>

## List directory contents with filters

It's possible to restrict a `Directory` to a subset of directory entries, by specifying a list of filename patterns to include or exclude.

The following example shows how to obtain a reference to the host working directory containing only `*.rar` files:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/work-with-host-filesystem/list-dir-include/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/work-with-host-filesystem/list-dir-include/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/work-with-host-filesystem/list-dir-include/main.py
```

</TabItem>
</Tabs>

The following example shows how to obtain a reference to the host working directory containing all files except `*.txt` files:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/work-with-host-filesystem/list-dir-exclude/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/work-with-host-filesystem/list-dir-exclude/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/work-with-host-filesystem/list-dir-exclude/main.py
```

</TabItem>
</Tabs>

The exclusion pattern overrides the inclusion pattern, but not vice-versa. The following example demonstrates by obtaining a reference to the host working directory containing all files except `*.rar` files:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/work-with-host-filesystem/list-dir-exclude-include/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/work-with-host-filesystem/list-dir-exclude-include/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/work-with-host-filesystem/list-dir-exclude-include/main.py
```

</TabItem>
</Tabs>

## Mount a host directory in a container

A common operation when working with containers is to mount a host directory to a path in the container and then perform operations on it. It is necessary to provide the mount point in the container and the directory to be mounted as method arguments.

The following example shows how to mount a host directory in a container at the `/host` container path and then execute a command in the container referencing the mounted directory:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/work-with-host-filesystem/mount-dir/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/work-with-host-filesystem/mount-dir/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/work-with-host-filesystem/mount-dir/main.py
```

</TabItem>
</Tabs>

## Export a directory from a container to the host

A directory can be exported to a different path. The destination path is supplied to the method as an argument.

The following example creates a file in a container's `/tmp` directory and then exports the contents of that directory to the host's temporary directory:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/work-with-host-filesystem/export-dir/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/work-with-host-filesystem/export-dir/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/work-with-host-filesystem/export-dir/main.py
```

</TabItem>
</Tabs>

## Important notes

Using the host filesystem in your Dagger pipeline is convenient, but there are some important considerations to keep in mind:

- If a file loaded from the host changes even slightly (including minor changes such as a timestamp change with the file contents left unmodified), then the Dagger cache will be invalidated. An extremely common source of invalidations occurs when loading the `.git` directory from the host filesystem, as that directory will change frequently, including when there have been no actual changes to any source code.

  :::tip
  To maximize cache re-use, it's important to use the include/exclude options for local directories to only include the files/directories needed for the pipeline. Excluding the `.git` directory is highly advisable unless there's a strong need to be able to perform Git operations on top of the loaded directory inside Dagger.
  :::

- The host directory is synchronized into the Dagger Engine similar to `rsync` or `scp`; it's not a "bind mount". This means that any change you make to the loaded directory in your Dagger pipeline will not result in a change to the directory on the host.

  :::warning
  If you want the changes made to a loaded local directory inside a Dagger pipeline to be reflected on the host, it needs to be explictly exported to the host. However, this should be approached with caution, since any overlap in the files being exported with the files on the host will result in the host files being overwritten.
  :::

- Synchronization of a local directory happens once per Dagger client instance (in user-facing terms, once per `dagger.Connect` call in the Dagger SDKs). This means that if you load the local directory, then make changes to it on the host, those changes will not be reloaded within the context of a single Dagger client. Furthermore, due to lazy executions, the loading happens the first time the directory is used in a non-lazy operation.

  :::tip
  It's safest to not modify a loaded host directory at all while a Dagger client is running, as otherwise it is hard to predict what will be loaded.
  :::

## Conclusion

This guide introduced you to the functions available in the Dagger SDKs to work with the host filesystem. It provided explanations and code samples demonstrating how to set the host working directory, read directory contents (with and without pathname filters), mount a host directory in a container and export a directory from a container to the host.

Use the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
