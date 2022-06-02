---
slug: /1203/client
displayed_sidebar: '0.2'
---

# Interacting with the client

`dagger.#Project` has a `client` field that allows interaction with the local machine where the `dagger` command line client is run. You can:

- Read and write files and directories;
- Use local sockets;
- Load environment variables;
- Run commands;
- Get current platform.

## Accessing the file system

You may need to load a local directory as a `dagger.#FS` type in your project:

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

:::tip
You can also capture `stderr` for errors and provide `stdin` for input.
:::

## Platform

If you need the current platform though, there’s a more portable way than running `uname` like in the previous example:

```cue file=../tests/core-concepts/client/plans/platform.cue

```
