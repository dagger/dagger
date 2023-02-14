---
slug: /110632/embed-directories
displayed_sidebar: "current"
category: "guides"
tags: ["go"]
authors: ["Guillaume de Rouville"]
date: "04/11/2022"
---

# Copy Embedded Directories into a Container

Dagger does not expose the option to copy entire directories as a single step (yet), whether it is between containers or from an embedded directory to a container. It is, however, doable by traversing the directory tree.

Assume that you have a Dagger CI tool containing the following code structure, which contains an example directory:

```shell
tree
.
├── go.mod
├── go.sum
├── main.go
└── example
    └── foo.go
```

The following example demonstrates how to copy an embedded directory:

```go file=./snippets/embed-directories/main.go
```

Attempt to run the code and print the content of the `/embed` directory:

```shell
➜  go run .
/embed/:
total 4
drwxr-xr-x    1 root     root          4096 Oct 31 16:49 example

/embed/example:
total 4
-rw-r--r--    1 root     root            50 Oct 31 16:49 foo.go
```

In this case, the function succeeds in copying the embedded `example` directory.

:::warning
You may encounter errors if your directory contains +1000 files, due to the concatenation of the queries.
:::
