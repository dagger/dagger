:::tip
The `dagger` CLI is available for installation on macOS, Linux, and Windows to run locally or in a CI environment.
:::

Install the `dagger` CLI following the steps below.

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
curl -L https://dl.dagger.io/dagger/install.sh | VERSION=0.3.6 sh

./bin/dagger version
dagger 0.3.6 (GIT_SHA) darwin/arm64
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

If you want to install the Dagger Engine to a different location, `cd` where you want `./bin/dagger` in.

If you want to install a specific version of `dagger`, you can run:

```shell
curl -L https://dl.dagger.io/dagger/install.sh | VERSION=0.3.6 sh

./bin/dagger version
dagger 0.3.6 (GIT_SHA) linux/amd64
```

</TabItem>

<TabItem value="windows">

`dagger` can be installed in Windows via an installation PowerShell script.

If you want to use the installation script, PowerShell 7.0 or newer is required. From PowerShell, run:

```Powershell
Invoke-WebRequest -UseBasicParsing -Uri https://dl.dagger.io/dagger/install.ps1 | Invoke-Expression
```

If you want to install dagger to a different location, pass in a location to the script with the `-InstallPath` parameter.

```Powershell
$script = Invoke-WebRequest -UseBasicParsing -Uri https://dl.dagger.io/dagger/install.ps1
$params = "-InstallPath C:\temp"
"$script $params" | Invoke-Expression
```

If you want to install a specific version of `dagger`, pass in a version number with the `-DaggerVersion` parameter.

```Powershell
$script = Invoke-WebRequest -UseBasicParsing -Uri https://dl.dagger.io/dagger/install.ps1
$params = "-DaggerVersion 0.3.6"
"$script $params" | Invoke-Expression
```

Without passing in the `-InstallPath`, We'll save everything under `<your home folder>\dagger`

Check that `dagger` is installed correctly by opening a `Command Prompt` terminal and run:

```shell
where dagger
C:\<your home folder>\dagger\dagger.exe
```

</TabItem>

</Tabs>
}

</BrowserOnly>
