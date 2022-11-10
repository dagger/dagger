import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";
import BrowserOnly from "@docusaurus/BrowserOnly";

:::tip
The `dagger-cue` CLI is available for installation on macOS, Linux, and Windows to run locally or in a CI environment.
:::

Install the `dagger-cue` CLI following the steps below.

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
If you do, you can install `dagger-cue` with a single command:

```shell
brew install dagger-cue
```

This installs `dagger-cue` in:

```shell
type dagger-cue
# macOS ARM:
dagger-cue is /opt/homebrew/bin/dagger-cue
# macOS Intel:
dagger-cue is /usr/local/bin/dagger-cue
```

If you do not have Homebrew installed, or you want to install a specific version of `dagger-cue`, you can run:

```shell
curl -L https://dl.dagger.io/dagger-cue/install.sh | VERSION=0.2.232 sh

./bin/dagger-cue version
dagger-cue 0.2.232 (GIT_SHA) darwin/arm64
```

</TabItem>

<TabItem value="linux">

The quickest way of installing `dagger-cue` on Linux is to run the following command:

```shell
cd /usr/local
curl -L https://dl.dagger.io/dagger-cue/install.sh | sh
```

This installs `dagger-cue` in `/usr/local/bin`:

```shell
type dagger-cue
dagger-cue is /usr/local/bin/dagger-cue
```

If you want to install the Dagger Engine to a different location, `cd` where you want `./bin/dagger-cue` in.

If you want to install a specific version of `dagger-cue`, you can run:

```shell
curl -L https://dl.dagger.io/dagger-cue/install.sh | VERSION=0.2.232 sh

./bin/dagger-cue version
dagger-cue 0.2.232 (GIT_SHA) linux/amd64
```

</TabItem>

<TabItem value="windows">

`dagger-cue` can be installed in Windows via an installation powershell script.

If you want to use the installation script, powershell 7.0 or newer is required. From powershell, run:

```Powershell
Invoke-WebRequest -UseBasicParsing -Uri https://dl.dagger.io/dagger-cue/install.ps1 | Invoke-Expression
```

If you want to install dagger to a different location, pass in a location to the script with the `-InstallPath` parameter.

```Powershell
$script = Invoke-WebRequest -UseBasicParsing -Uri https://dl.dagger.io/dagger-cue/install.ps1
$params = "-InstallPath C:\temp"
"$script $params" | Invoke-Expression
```

If you want to install a specific version of `dagger-cue`, pass in a version number with the `-DaggerVersion` parameter.

```Powershell
$script = Invoke-WebRequest -UseBasicParsing -Uri https://dl.dagger.io/dagger-cue/install.ps1
$params = "-DaggerVersion 0.2.232"
"$script $params" | Invoke-Expression
```

Without passing in the `-InstallPath`, We'll save everything under `<your home folder>\dagger-cue`

Check that `dagger-cue` is installed correctly by opening a `Command Prompt` terminal and run:

```shell
where dagger-cue
C:\<your home folder>\dagger\dagger-cue.exe
```

</TabItem>

</Tabs>
}

</BrowserOnly>
