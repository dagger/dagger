---
title: SecretsUsedInArgOrEnv
description: Sensitive data should not be used in the ARG or ENV commands
aliases:
  - /go/dockerfile/rule/secrets-used-in-arg-or-env/
---

## Output

```text
Potentially sensitive data should not be used in the ARG or ENV commands
```

## Description

While it is common to pass secrets to running processes
through environment variables during local development,
setting secrets in a Dockerfile using `ENV` or `ARG`
is insecure because they persist in the final image.
This rule reports violations where `ENV` and `ARG` keys
indicate that they contain sensitive data.

Instead of `ARG` or `ENV`, you should use secret mounts,
which expose secrets to your builds in a secure manner,
and do not persist in the final image or its metadata.
See [Build secrets](https://docs.docker.com/build/building/secrets/).

## Examples

❌ Bad: `AWS_SECRET_ACCESS_KEY` is a secret value.

```dockerfile
FROM scratch
ARG AWS_SECRET_ACCESS_KEY
```

