---
slug: /421437/work-with-host-filesystem
displayed_sidebar: 'current'
category: "guides"
tags: ["go"]
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

## Set the host working directory

The easiest way to set the working directory on the host for your CI pipeline is at the time of client instantiation, as a client configuration option. The following example shows how to set the host working directory:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/work-with-host-filesystem/set-workdir/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/work-with-host-filesystem/set-workdir/index.mts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/work-with-host-filesystem/set-workdir/main.py
```

</TabItem>
</Tabs>

## List directory contents

The following example shows how to list the contents of the host working directory:

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

## List directory contents with filters

It's possible to restrict a directory list to a subset of directory entries, by specifying a list of filename patterns to include or exclude.

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

## Conclusion

This guide introduced you to the functions available in the Dagger SDKs to work with the host filesystem. It provided explanations and code samples demonstrating how to set the host working directory, read directory contents (with and without pathname filters), mount a host directory in a container and export a directory from a container to the host.

Use the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
