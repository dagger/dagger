---
slug: /1228/handling-outputs
displayed_sidebar: 0.2
---

# Handling action outputs

Dagger tries to detect which fields are outputs in an action. Simple values like strings, numbers and booleans are printed directly to the console, as you can see when the [todo app example](../../getting-started/1200-local-dev.md) finishes:

```shell
➜  APP_NAME=dagger-todo dagger do deploy
[✔] actions.deps
[✔] actions.test.script
[✔] client.env
[✔] actions.build.run.script
[✔] actions.deploy.container.script
[✔] client.filesystem."./".read
[✔] actions.deploy
[✔] actions.test
[✔] actions.build.run
[✔] actions.build.contents
[✔] actions.deploy.container
[✔] client.filesystem."./_build".write
[✔] actions.deploy.container.export
Field      Value
url        "https://dagger-todo.netlify.app"
deployUrl  "https://62698983ffe8661d60613431--dagger-todo.netlify.app"
logsUrl    "https://app.netlify.com/sites/dagger-todo/deploys/62698983ffe8661d60613431"

```

This is very useful to get immediate feedback on an action's results.

## Piping a result

Besides the `plain` format (the default), you can also use `json` or `yaml`. JSON is particularly useful if you want to pipe a result into another process:

:::tip
For this example, ensure you have a registry on `localhost` listening on port `5042`:

```shell
➜ docker run -d -p 5042:5000 --restart=always --name localregistry registry:2
```

:::

```cue file=../../tests/guides/handling-outputs/default.cue
```

```shell
➜ dagger --output-format json do push | jq '.result'
"localhost:5042/alpine:latest@sha256:a777c9c66ba177ccfea23f2a216ff6721e78a662cd17019488c417135299cd89"
```

:::tip
You can silence the `info` logs by raising the log level (or redirecting _stderr_ somewhere else):

```shell
➜ dagger -l error --output-format json do push | jq '.result'
```

:::

## Saving into a file

You can also save the output to a file using the `--output` flag. Let's do it in _yaml_ this time:

```shell
➜ dagger --output-format yaml --output result.yaml do push
➜ cat result.yaml
result: localhost:5042/alpine:latest@sha256:47a163eb7b572819d862b4a2c95a399829c8c79fab51f1d40c59708aa0e35331
```

## Controlling the output

You're not limited to the outputs of an action because you can make your own in a wrapper action:

```cue file=../../tests/guides/handling-outputs/wrapper.cue
```

```shell
➜ dagger do push
[✔] actions.pull
[✔] actions.push
Field   Value
digest  "localhost:5042/alpine:latest@sha256:47a163eb7b572819d862b4a2c95a399829c8c79fab51f1d40c59708aa0e35331"
path    "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
```

## Full output control

Since you can only output simple values, you may find the need for a solution where you can output more complex types such as structs and lists. As showcased in the [interacting with the client](../../core-concepts/1203-client.md) docs, Dagger has the ability to write into the client filesystem through the `client` API.

Using this capability we can then have full control of what to output. The downsides are that these won't print to the console (only to a file), and you won't be able to pipe directly from the `dagger` command.

Let's leverage CUE's [default integrations](https://cuelang.org/docs/integrations/) and marshal a more complex value into a single `json` or `yaml` file.

```cue file=../../tests/guides/handling-outputs/control.cue
```

```shell
➜ dagger do pull
[✔] actions.pull
[✔] client.filesystem."config.yaml".write
➜ cat config.yaml
env:
  PATH: /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
cmd:
  - /bin/sh
```
