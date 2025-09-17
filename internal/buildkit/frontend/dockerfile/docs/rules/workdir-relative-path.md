---
title: WorkdirRelativePath
description: Relative workdir without an absolute workdir declared within the build can have unexpected results if the base image changes
aliases:
  - /go/dockerfile/rule/workdir-relative-path/
---

## Output

```text
Relative workdir 'app/src' can have unexpected results if the base image changes
```

## Description

When specifying `WORKDIR` in a build stage, you can use an absolute path, like
`/build`, or a relative path, like `./build`. Using a relative path means that
the working directory is relative to whatever the previous working directory
was. So if your base image uses `/usr/local/foo` as a working directory, and
you specify a relative directory like `WORKDIR build`, the effective working
directory becomes `/usr/local/foo/build`.

The `WorkdirRelativePath` build rule warns you if you use a `WORKDIR` with a
relative path without first specifying an absolute path in the same Dockerfile.
The rationale for this rule is that using a relative working directory for base
image built externally is prone to breaking, since working directory may change
upstream without warning, resulting in a completely different directory
hierarchy for your build.

## Examples

❌ Bad: this assumes that `WORKDIR` in the base image is `/`
(if that changes upstream, the `web` stage is broken).

```dockerfile
FROM nginx AS web
WORKDIR usr/share/nginx/html
COPY public .
```

✅ Good: a leading slash ensures that `WORKDIR` always ends up at the desired path.

```dockerfile
FROM nginx AS web
WORKDIR /usr/share/nginx/html
COPY public .
```

