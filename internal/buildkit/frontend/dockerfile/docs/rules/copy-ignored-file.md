---
title: CopyIgnoredFile
description: Attempting to Copy file that is excluded by .dockerignore
aliases:
  - /go/dockerfile/rule/copy-ignored-file/
---

> [!NOTE]
> This check is experimental and is not enabled by default. To enable it, see
> [Experimental checks](https://docs.docker.com/go/build-checks-experimental/).

## Output

```text
Attempting to Copy file "./tmp/Dockerfile" that is excluded by .dockerignore
```

## Description

When you use the Add or Copy instructions from within a Dockerfile, you should
ensure that the files to be copied into the image do not match a pattern
present in `.dockerignore`.

Files which match the patterns in a `.dockerignore` file are not present in the
context of the image when it is built. Trying to copy or add a file which is
missing from the context will result in a build error.

## Examples

With the given `.dockerignore` file:

```text
*/tmp/*
```

❌ Bad: Attempting to Copy file "./tmp/Dockerfile" that is excluded by .dockerignore

```dockerfile
FROM scratch
COPY ./tmp/helloworld.txt /helloworld.txt
```

✅ Good: Copying a file which is not excluded by .dockerignore

```dockerfile
FROM scratch
COPY ./forever/helloworld.txt /helloworld.txt
```

