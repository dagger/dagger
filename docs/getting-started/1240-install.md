---
slug: /install
displayed_sidebar: "0.2"
---

# Install Dagger

Install dagger on your local machine.

import Tabs from '@theme/Tabs'; import TabItem from '@theme/TabItem';
import BrowserOnly from '@docusaurus/BrowserOnly';

<BrowserOnly>
{() =>
<Tabs defaultValue={
 window.navigator.userAgent.indexOf('Linux') != -1 ? 'linux':
 window.navigator.userAgent.indexOf('Win') != -1 ? 'windows':
 'macos'}
groupId="os"
values={[
{label: 'macOS', value: 'macos'}, {label: 'Linux', value: 'linux'}, {label: 'Windows', value: 'windows'},
]}>

<TabItem value="macos">

We assume that you have [Homebrew](https://brew.sh/) installed.
If you do, you can install `dagger` with a single command:

```shell
brew install dagger/tap/dagger
```

This installs `dagger` in:

```shell
type dagger
# macOS ARM:
dagger is /opt/homebrew/bin/dagger
# macOS Intel:
dagger is /usr/local/bin/dagger
```

If you do not have Homebrew installed, or you want to install a specific version of `dagger`, you can run:

```shell
curl -L https://dl.dagger.io/dagger/install.sh | DAGGER_VERSION=0.2.11 sh

./bin/dagger version
dagger 0.2.11 (GIT_SHA) darwin/arm64
```

</TabItem>

<TabItem value="linux">

The quickest way of installing `dagger` on Linux is to run the following command:

```shell
cd /usr/local
curl -L https://dl.dagger.io/dagger/install.sh | sh
```

This installs `dagger` in `/usr/local/bin`:

```shell
type dagger
dagger is /usr/local/bin/dagger
```

If you want to install dagger to a different location, `cd` where you want `./bin/dagger` in.

If you want to install a specific version of `dagger`, you can run:

```shell
curl -L https://dl.dagger.io/dagger/install.sh | DAGGER_VERSION=0.2.11 sh

./bin/dagger version
dagger 0.2.11 (GIT_SHA) linux/amd64
```

</TabItem>

<TabItem value="windows">

`dagger` can be installed in Windows via a install powershell script, [Chocolatey](https://community.chocolatey.org/packages/dagger) or [Scoop](https://scoop.sh/#/apps?q=dagger).

If you want to use the install script, from a powershell terminal, run:

```shell
Invoke-WebRequest -UseBasicParsing -Uri https://dl.dagger.io/dagger/install.ps1 | Invoke-Expression
```

We'll save everything under `<your home folder>/dagger`

Check that `dagger` is installed correctly by opening a `Command Prompt` terminal and run:

```shell
where dagger
C:\<your home folder>\dagger.exe
```

If you have Chocolatey installed, just open a terminal and run:

```shell
choco install dagger
```

If you have Scoop installed, just open a terminal and run:

```shell
scoop bucket add main # If you don't have the main bucket added yet
scoop install dagger
```

</TabItem>

</Tabs>
}

</BrowserOnly>
