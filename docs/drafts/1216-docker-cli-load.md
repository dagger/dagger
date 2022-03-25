---
slug: /1216/docker-cli-load
displayed_sidebar: europa
---

# Loading a dagger image into a docker daemon

Using `cli.#Load`, you can save a dagger image (`docker.#Image`) into a local or remote engine.

It can be useful to debug or test a build locally before pushing.

## Local daemon

```cue file=./plans/docker-cli-load/local.cue
```

## Remote daemon, via SSH

```cue file=./plans/docker-cli-load/ssh.cue
```
