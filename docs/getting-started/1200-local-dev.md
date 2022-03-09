---
slug: /
displayed_sidebar: europa
---

# CI/CD in your local dev

Everyone should be able to run their CI/CD pipeline locally.
Having to commit & push in order to test a change is a slow way to iterate on a pipeline.
This guide shows you the Dagger way.
Within 5 minutes, you will have a local CI/CD loop and run your first test & build pipeline.

import Tabs from '@theme/Tabs'; import TabItem from '@theme/TabItem';

<Tabs defaultValue="macos"
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

Check that `dagger` installed correctly by verifying its execution path:

```shell
type dagger
dagger is /opt/homebrew/bin/dagger
```

Before we can build & test our example app with `dagger`, we need to have Docker running.
You most likely already have Docker set up.
If not, [Docker Desktop](https://www.docker.com/products/docker-desktop) makes this easy.
With Docker running, we are ready to download our example app and run its CI/CD pipeline:

```shell
git clone https://github.com/dagger/dagger
cd dagger
git checkout v0.2.0-beta.1

cd pkg/universe.dagger.io/examples/todoapp
```

With everything in place, we run the CI/CD pipeline locally:

```shell
dagger do build
```

With an empty cache, installing all dependencies, then testing & generating a build for this example app completes in just under 3 minutes:

```shell
[✔] client.filesystem.".".read                                    0.1s
[✔] actions.deps                                                118.8s
[✔] actions.test.script                                           0.1s
[✔] actions.test                                                  6.3s
[✔] actions.build.run.script                                      0.0s
[✔] actions.build.run                                            43.7s
[✔] actions.build.contents                                        0.4s
[✔] client.filesystem.build.write                                 0.1s
```

Since this is a static application, we can open the files which are generated in `actions.build.contents` in a browser.
The last step copies the build result into the `build` directory on the host.
On macOS, we run `open build/index.html` in our terminal and see the following app preview:

![todoapp preview](/img/getting-started/todoapp.png)

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

[✔] client.filesystem.".".read                                    0.0s
[✔] actions.deps                                                 40.8s
[✔] actions.test.script                                           0.0s
[✔] actions.test                                                  6.3s
[✔] actions.build.run.script                                      0.0s
[✔] actions.build.run                                            43.6s
[✔] actions.build.contents                                        0.4s
[✔] client.filesystem.build.write                                 0.1s
```

The total `84.4` time is macOS specific, since the Linux alternative is more than 5x quicker.
Either way, this local test & build loop is likely to change our approach to iterating on changes.
It becomes even more obvious when the change is not as straightforward as knowing _exactly_ which line to edit.

</TabItem>

<TabItem value="linux">

[Download the latest 64bit Linux binary release](https://github.com/dagger/dagger/releases/latest).
We support both x86 & ARM architectures.

We install `dagger` in `/usr/local/bin`, but anywhere in your `PATH` works:

```shell
type dagger
dagger is /usr/local/bin/dagger
```

Before we can build, test & deploy our example app with `dagger`, we need to have Docker Engine running.
You most likely already have Docker Engine set up.
If not, [Docker on Linux install](https://docs.docker.com/engine/install/#server) makes this easy.
With Docker Engine running, we are ready to download our example app and run its CI/CD pipeline:

```shell
git clone https://github.com/dagger/dagger
cd dagger
git checkout v0.2.0-beta.1

cd pkg/universe.dagger.io/examples/todoapp
```

With everything in place, we run the CI/CD pipeline locally:

```shell
dagger do build
```

With an empty cache, installing all dependencies, then testing & generating a build for this example app completes in just under 1 minute:

```shell
[✔] client.filesystem.".".read                                    0.3s
[✔] actions.deps                                                 39.7s
[✔] actions.test.script                                           0.2s
[✔] actions.test                                                  1.9s
[✔] actions.build.run.script                                      0.1s
[✔] actions.build.run                                            10.0s
[✔] actions.build.contents                                        0.6s
[✔] client.filesystem.build.write                                 0.1s
```

Since this is a static application, we can open the files which are generated in `actions.build.contents` in a browser.
The last step copies the build result into the `build` directory on the host.
On Linux, we run `xdg-open build/index.html` in our terminal and see the following app preview:

![todoapp preview](/img/getting-started/todoapp.png)

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

[✔] client.filesystem.".".read                                    0.1s
[✔] actions.deps                                                 13.3s
[✔] actions.test.script                                           0.0s
[✔] actions.test                                                  1.8s
[✔] actions.build.run.script                                      0.0s
[✔] actions.build.run                                            10.1s
[✔] actions.build.contents                                        0.6s
[✔] client.filesystem.build.write                                 0.1s
```

Being able to re-run the test & build loop locally in `26.7s`, without adding any extra dependencies to our host, is likely to change our approach to iterating on changes.
It becomes even more obvious when the change is not as straightforward as knowing _exactly_ which line to edit.

</TabItem>

<TabItem value="windows">

We assume that you have [curl](https://curl.se/windows/) installed.
If you do, you can install `dagger` with a single command. From a powershell terminal, run the following command:

```shell
curl https://releases.dagger.io/dagger/install.ps1 -OutFile install.ps1 ; ./install.ps1; rm install.ps1
```

We try to move the dagger binary under `C:\Windows\System32` but
in case we miss the necessary permissions, we'll save everything under `<your home folder>/dagger`

Check that `dagger` is installed correctly by opening a command prompt and run:

```shell
where dagger
C:\<your home folder>\dagger.exe
```

</TabItem>
</Tabs>

:::tip
Now that we are comfortable with our local CI/CD loop, let us configure a remote CI environment in the second part.
The difference is that we will also deploy the build output to Netlify.
Dagger makes this easy.
:::
