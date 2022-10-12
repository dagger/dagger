---
slug: /1217/docker-cli-run
displayed_sidebar: '0.2'
---

# Running commands with the docker binary (CLI)

There's a `universe.dagger.io/docker/cli` package that allows you to run docker commands against a local or remote docker engine. Here's a few examples.

## Local daemon

```cue file=../../plans/docker-cli-run/local.cue

```

## Remote daemon, via SSH

```cue file=../../plans/docker-cli-run/ssh.cue

```

## Remote daemon, via HTTPS

```cue file=../../plans/docker-cli-run/tcp.cue

```
