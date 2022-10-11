---
slug: /1203/client
displayed_sidebar: '0.2'
---

# Interacting with the client

`dagger.#Plan` has a `client` field that allows interaction with the local machine where the `dagger` command line client is run. You can:

- Read and write files and directories;
- Use local sockets;
- Load environment variables;
- Run commands;
- Get current platform.

## Accessing the file system

You may need to load a local directory as a `dagger.#FS` type in your plan:

```cue file=../tests/core-concepts/client/plans/fs.cue

```

It’s also easy to write a file locally.

Strings can be written to local files like this:

```cue file=../tests/core-concepts/client/plans/file.cue

```

:::caution
Strings in CUE are UTF-8 encoded, so the above example should never be used when handling arbitrary binary data. There is also a limit on the size of these strings (current 16MB). The next example of exporting a `dagger.#FS` shows how to handle the export of files of arbitrary size and encoding.
:::

Files and directories (in the form of a `dagger.#FS`) can be exported to the local filesystem too:

```cue file=../tests/core-concepts/client/plans/file_export.cue

```

## Using a local socket

You can use a local socket in an action:

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';
import BrowserOnly from '@docusaurus/BrowserOnly';

<BrowserOnly>
  {() =>
<Tabs defaultValue={ window.navigator.userAgent.indexOf('Win') != -1 ? 'windows': 'unix'} groupId="client-env">

<TabItem value="unix" label="Linux/macOS">

```cue file=../tests/core-concepts/client/plans/unix.cue

```

</TabItem>

<TabItem value="windows" label="Windows">

```cue file=../tests/core-concepts/client/plans/windows.cue

```

</TabItem>
</Tabs>
  }
</BrowserOnly>

## Environment variables

Environment variables can be read from the local machine as strings or secrets, just specify the type:

```cue file=../tests/core-concepts/client/plans/env.cue

```

You can provide a default value for strings, or mark any environment variable as optional so they don't fail if not defined in the host:

```cue file=../tests/core-concepts/client/plans/env_optional.cue

```

## Running commands

Sometimes you need something more advanced that only a local command can give you:

```cue file=../tests/core-concepts/client/plans/cmd.cue

```

```shell title="Output"
➜  dagger do test
[✔] client.commands.arch
[✔] client.commands.os
[✔] actions.test
Field  Value
os     "Darwin"
arch   "x86_64"
```

:::tip

There's a more portable way to find the OS and CPU architecture, just use the [client's platform](#platform).

:::

:::tip

To learn more about controlling action outputs, see the [Handling action outputs](../guides/actions/1228-handling-outputs.md#controlling-the-output) guide.

:::

### Standard input

If your command needs to read from the standard input stream, you can use `stdin`:

```cue file=../tests/core-concepts/client/plans/cmd_stdin.cue
```

### Capturing errors

:::caution Attention

A failing exit code will fail the plan, so if you need to further debug the cause of a failed command, you can just try running it directly in your computer. Some commands print to `stderr` for messages that aren't fatal. This is for those cases.

:::

If you need the *stderr* output of a command in an action, you can capture it with `stderr`:

```cue file=../tests/core-concepts/client/plans/cmd_stderr.cue
```

```shell title="Output"
Field  Value
error  "cat: /foobar: No such file or directory"```
```

### Secrets

All input/output streams (`stdout`, `stderr` and `stdin`) accept a `dagger.#Secret` instead of a `string`. You can see a simple example using [SOPS](../core-concepts/1204-secrets.md#sops).

It may be useful to use a secret as an input to a command as well:

```cue file=../tests/core-concepts/client/plans/cmd_secret.cue
```

```shell title="Output"
Field   Value
digest  "aec070645fe53ee3b3763059376134f058cc337247c978add178b6ccdfb0019f"
```

Another use case is needing to provide a password from input to a command.

## Platform

If you need the current platform, there’s a more portable way than running the `uname` command:

```cue file=../tests/core-concepts/client/plans/platform.cue
```

```shell title="dagger --log-format plain do test"
INFO  actions.test._run._exec | #4 0.209 Platform: darwin / amd64
```

:::tip Remember

This is the platform where the `dagger` binary is being run (a.k.a *client*), which is different from the environment where the action is actually run (i.e., BuildKit, a.k.a *server*).

:::

:::tip

If `client: _` confuses you, see [Use top to match anything](../guidelines/1226-coding-style.md#use-top-to-match-anything).

:::

:::tip

You can see an example of this being used in our own [CI dagger plan](https://github.com/dagger/dagger/blob/main/ci.cue) in the build action, to specify the `os` and `arch` fields in [`go.#Build`](https://github.com/dagger/dagger/blob/main/pkg/universe.dagger.io/go/build.cue):

```cue
build: go.#Build & {
  source:  _source
  os:      client.platform.os
  arch:    client.platform.arch
  ...
}
```

:::
