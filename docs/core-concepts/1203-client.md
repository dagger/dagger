---
slug: /1203/client
displayed_sidebar: europa
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

It’s also easy to write a file locally:

```cue file=../tests/core-concepts/client/plans/file.cue
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
