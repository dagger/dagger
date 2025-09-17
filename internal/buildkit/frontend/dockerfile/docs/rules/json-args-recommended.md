---
title: JSONArgsRecommended
description: JSON arguments recommended for ENTRYPOINT/CMD to prevent unintended behavior related to OS signals
aliases:
  - /go/dockerfile/rule/json-args-recommended/
---

## Output

```text
JSON arguments recommended for ENTRYPOINT/CMD to prevent unintended behavior related to OS signals
```

## Description

`ENTRYPOINT` and `CMD` instructions both support two different syntaxes for
arguments:

- Shell form: `CMD my-cmd start`
- Exec form: `CMD ["my-cmd", "start"]`

When you use shell form, the executable runs as a child process to a shell,
which doesn't pass signals. This means that the program running in the
container can't detect OS signals like `SIGTERM` and `SIGKILL` and respond to
them correctly.

## Examples

❌ Bad: the `ENTRYPOINT` command doesn't receive OS signals.

```dockerfile
FROM alpine
ENTRYPOINT my-program start
# entrypoint becomes: /bin/sh -c my-program start
```

To make sure the executable can receive OS signals, use the exec form for `CMD`
and `ENTRYPOINT`, which lets you run the executable as the main process (`PID
1`) in the container, avoiding a shell parent process.

✅ Good: the `ENTRYPOINT` receives OS signals.

```dockerfile
FROM alpine
ENTRYPOINT ["my-program", "start"]
# entrypoint becomes: my-program start
```

Note that running programs as PID 1 means the program now has the special
responsibilities and behaviors associated with PID 1 in Linux, such as reaping
child processes.

Alternatively, if you want to ignore this lint rule because you do want your
executable to be invoked via a shell, you can use the
[`SHELL`](https://docs.docker.com/reference/dockerfile/#shell) Dockerfile
instruction to explicitly specify a shell to use.

✅ Good: shell is explicitly defined.

```dockerfile
FROM alpine
RUN apk add bash
SHELL ["/bin/bash", "-c"]
ENTRYPOINT echo "hello world"
```

