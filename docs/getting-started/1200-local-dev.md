---
slug: /1200/local-dev
displayed_sidebar: '0.2'
---

# CI/CD in your local dev

Everyone should be able to develop, test and run their CI/CD pipeline locally.
Having to commit & push in order to test a change slows down iteration.
This guide shows you the Dagger way.
Within 5 minutes, you will have a local CI/CD loop and run your first test & build pipeline.

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
curl -L https://dl.dagger.io/dagger/install.sh | DAGGER_VERSION=0.2.4 sh

./bin/dagger version
dagger 0.2.4 (GIT_SHA) darwin/arm64
```

Before we can build & test our example app with `dagger`, we need to have Docker running.
You most likely already have Docker set up.
If not, [Docker Desktop](https://www.docker.com/products/docker-desktop) makes this easy.

With Docker running, we are ready to download our example app and run its CI/CD pipeline locally:

```shell
git clone https://github.com/dagger/dagger
cd dagger
git checkout v0.2.4

cd pkg/universe.dagger.io/examples/todoapp
dagger do build
```

With an empty cache, installing all dependencies, then testing & generating a build for this example app completes in just under 3 minutes:

```shell
[✔] client.filesystem."./".read                                   0.1s
[✔] actions.deps                                                118.8s
[✔] actions.test.script                                           0.1s
[✔] actions.test                                                  6.3s
[✔] actions.build.run.script                                      0.0s
[✔] actions.build.run                                            43.7s
[✔] actions.build.contents                                        0.4s
[✔] client.filesystem."./_build".write                            0.1s
```

Since this is a static application, we can open the files which are generated in `actions.build.contents` in a browser.
The last step - `client.filesystem.build.write` - copies the build result into the `_build` directory on the host.

On macOS, we run `open _build/index.html` in our terminal and see the following app preview:

![todoapp preview](/img/getting-started/todoapp.macos.png)

One of the big advantages to this approach is that we did not have to install any dependencies specific to this application.
Dagger managed all the intermediary steps, and we ended up with the end-result on our host, without any of the transient dependencies.

Now that we have everything running locally, let us make a change and get a feel for our local CI/CD loop.
The quicker we can close this loop, the quicker we can learn what actually works.
With Dagger, we can close this loop locally, without committing and pushing our changes.
And since every action is cached, subsequent runs will be quicker.

In the todoapp directory, edit line `25` of `src/components/Form.js` and save the file.

I change this line to `What must be done today?` and run the build locally again:

```shell
dagger do build

[✔] client.filesystem."./".read                                   0.0s
[✔] actions.deps                                                  7.5s
[✔] actions.test.script                                           0.0s
[✔] actions.test                                                  6.0s
[✔] actions.build.run.script                                      0.0s
[✔] actions.build.run                                            29.2s
[✔] actions.build.contents                                        0.0s
[✔] client.filesystem."./_build".write                            0.1s
```

The total `42.8` time is macOS specific, since the Linux alternative is more than 8x quicker.
Either way, this local test & build loop is likely to change our approach to iterating on changes.
It becomes even more obvious when the change is not as straightforward as knowing _exactly_ which line to edit.

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
curl -L https://dl.dagger.io/dagger/install.sh | DAGGER_VERSION=0.2.4 sh

./bin/dagger version
dagger 0.2.4 (GIT_SHA) linux/amd64
```

Before we can build, test & deploy our example app with `dagger`, we need to have Docker Engine running.
You most likely already have Docker Engine set up.
If not, [install Docker Engine on Linux](https://docs.docker.com/engine/install/#server) makes this easy.

With Docker Engine running, we are ready to download our example app and run its CI/CD pipeline:

```shell
git clone https://github.com/dagger/dagger
cd dagger
git checkout v0.2.4

cd pkg/universe.dagger.io/examples/todoapp
dagger do build
```

With an empty cache, installing all dependencies, then testing & generating a build for this example app completes in just under 1 minute:

```shell
[✔] client.filesystem."./".read                                   0.3s
[✔] actions.deps                                                 39.7s
[✔] actions.test.script                                           0.2s
[✔] actions.test                                                  1.9s
[✔] actions.build.run.script                                      0.1s
[✔] actions.build.run                                            10.0s
[✔] actions.build.contents                                        0.6s
[✔] client.filesystem."./_build".write                            0.1s
```

Since this is a static application, we can open the files which are generated in `actions.build.contents` in a browser.
The last step - `client.filesystem.build.write` - copies the build result into the `_build` directory on the host.

On Linux, we run `xdg-open _build/index.html` in our terminal and see the following app preview:

![todoapp preview](/img/getting-started/todoapp.linux.png)

One of the big advantages to this approach is that we did not have to install any dependencies specific to this application.
Dagger managed all the intermediary steps, and we ended up with the end-result on our host, without any of the transient dependencies.

Now that we have everything running locally, let us make a change and get a feel for our local CI/CD loop.
The quicker we can close this loop, the quicker we can learn what actually works.
With Dagger, we can close this loop locally, without committing and pushing our changes.
And since every action is cached, subsequent runs will be quicker.

In the todoapp directory, edit line `25` of `src/components/Form.js` and save the file.

I change this line to `What must be done today?` and run the build locally again:

```shell
dagger do build

[✔] client.filesystem."./".read                                   0.0s
[✔] actions.deps                                                  1.1s
[✔] actions.test.script                                           0.0s
[✔] actions.test                                                  0.0s
[✔] actions.build.run.script                                      0.8s
[✔] actions.build.run                                             2.9s
[✔] actions.build.contents                                        0.0s
[✔] client.filesystem."./_build".write                             0.0s
```

Being able to re-run the test & build loop locally in `4.8s`, at the same speed as running `yarn` scripts locally and without adding any extra dependencies to our host, is likely to change our approach to iterating on changes.
It becomes even more obvious when the change is not as straightforward as knowing _exactly_ which line to edit.

</TabItem>

<TabItem value="windows">

From a powershell terminal, run:

```shell
Invoke-WebRequest -UseBasicParsing -Uri https://dl.dagger.io/dagger/install.ps1 | Invoke-Expression
```

We'll save everything under `<your home folder>/dagger`

Check that `dagger` is installed correctly by opening a `Command Prompt` terminal and run:

```shell
where dagger
C:\<your home folder>\dagger.exe
```

Before we can build & test our example app with `dagger`, we need to have Docker running.
You most likely already have Docker set up.
If not, [Docker Desktop](https://www.docker.com/products/docker-desktop) makes this easy.
With Docker running, we are ready to download our example app and run its CI/CD pipeline.
Still in your `Command Prompt` terminal:

```shell
git clone -c core.symlinks=true https://github.com/dagger/dagger
cd dagger
git checkout v0.2.4

cd pkg/universe.dagger.io/examples/todoapp
dagger do build
```

:::tip
By default, git on Windows does not automatically convert posix symbolic links, which explains the extra option `core.symlinks=true` while cloning the repository.

But you can also enable this once and for all in your git configuration, by running the following command from a Powershell terminal: `git config --global core.symlinks true`.
:::

With an empty cache, installing all dependencies, then testing & generating a build for this example app completes in just under a minute:

```shell
[✔] actions.deps                                                 62.1s
[✔] actions.build.run.script                                      0.4s
[✔] actions.test.script                                           0.5s
[✔] client.filesystem."./".read                                   0.6s
[✔] actions.test                                                  2.0s
[✔] actions.build.run                                            12.4s
[✔] actions.build.contents                                        0.1s
[✔] client.filesystem."./_build".write                            0.2s
```

Since this is a static application, we can open the files which are generated in `actions.build.contents` in a browser.
The last step - `client.filesystem.build.write` - copies the build result into the `_build` directory on the host.

On Windows, we run `start _build/index.html` in our `Command Prompt` terminal and see the following app preview:

![todoapp preview](/img/getting-started/todoapp.macos.png)

One of the big advantages to this approach is that we did not have to install any dependencies specific to this application.
Dagger managed all the intermediary steps, and we ended up with the end-result on our host, without any of the transient dependencies.

Now that we have everything running locally, let us make a change and get a feel for our local CI/CD loop.
The quicker we can close this loop, the quicker we can learn what actually works.
With Dagger, we can close this loop locally, without committing and pushing our changes.
And since every action is cached, subsequent runs will be quicker.

In the todoapp directory, edit line `25` of `src/components/Form.js` and save the file.

I change this line to `What must be done today?` and run the build locally again:

```shell
dagger do build
[✔] actions.build.run.script                                     0.0s
[✔] actions.deps                                                 3.4s
[✔] client.filesystem."./".read                                  0.1s
[✔] actions.test.script                                          0.0s
[✔] actions.test                                                 1.8s
[✔] actions.build.run                                            7.7s
[✔] actions.build.contents                                       0.2s
[✔] client.filesystem."./_build".write                           0.2s
```

Being able to re-run the test & build loop locally in `13.6s`, without adding any extra dependencies to our host, is likely to change our approach to iterating on changes.
It becomes even more obvious when the change is not as straightforward as knowing _exactly_ which line to edit.

</TabItem>

</Tabs>
}

</BrowserOnly>

:::tip
Now that we are comfortable with our local CI/CD loop, let us configure a remote CI environment in the second part.
The difference is that we will also deploy the build output to Netlify.
Dagger makes this easy.
:::
