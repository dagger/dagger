---
slug: /1216/docker-cli-load
displayed_sidebar: '0.2'
---

# Loading an image into a docker engine

Dagger can build, run, push and pull docker images natively, without the need of a Docker engine.
This feature is available in the package `universe.dagger.io/docker`.

However, sometimes after building an image, you specifically want to load it into your Docker engine.
This is possible with the *Docker CLI* package: `universe.dagger.io/docker/cli`.

Using `cli.#Load`, you can load an image built by Dagger into a local or remote engine.
It can be useful to debug or test a build locally before pushing.

## Local daemon

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';
import BrowserOnly from '@docusaurus/BrowserOnly';

<BrowserOnly>
  {() =>
<Tabs defaultValue={ window.navigator.userAgent.indexOf('Win') != -1 ? 'windows': 'unix'} groupId="local-daemon">

<TabItem value="unix" label="Linux/macOS">

```cue file=../../plans/docker-cli-load/local.cue

```

</TabItem>

<TabItem value="windows" label="Windows">

```cue file=../../plans/docker-cli-load/local_windows.cue

```

</TabItem>
</Tabs>
  }
</BrowserOnly>

## Remote daemon, via SSH

```cue file=../../plans/docker-cli-load/ssh.cue

```
