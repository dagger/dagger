---
slug: /1200/local-dev
displayed_sidebar: europa
---

# CI/CD in your local dev

Everyone should be able to run their CI/CD pipeline locally.
Having to commit & push in order to test a change is a slow way to iterate on a pipeline.
This guide shows you the Dagger way.
Within 5 minutes, you will have a local CI/CD loop and run your first build, test & deploy pipeline.

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

Before we can build, test & deploy our example app with `dagger`, we need to have Docker running.
You most likely already have Docker set up.
If not, [Docker Desktop](https://www.docker.com/products/docker-desktop) makes this easy.
With Docker running, we are ready to download our example app and use its dev CI/CD pipeline:

```shell
git clone https://github.com/dagger/examples
cd examples/todoapp
```

With everything in place, we run the CI/CD pipeline locally:

```shell
dagger up dev.cue
```

With an empty cache, installing all dependencies, then building, testing & deploying this example app completes in just under 3 minutes:

```shell
[✔] inputs.directories.app                                        0.3s
[✔] actions.test                                                125.6s
[✔] actions.build                                               163.0s
[+] actions.deploy                                              167.5s
#18 INFO: System: Ran is running on HTTP port 8020
#18 INFO: System: Listening on http://0.0.0.0:8020
```

:::caution
[localhost:8020](http://localhost:8020) is not accessible on macOS 12 & Docker 20.10.12. Works fine on Linux 🤷
:::

We can now access the application on [localhost:8020](http://localhost:8020) and get a preview of what the app would look like if the same thing ran in a CI environment.

While this is a good first step, it gets better when we run this again - the cache makes it quicker.
Type `^C` to exit the deployment, and run `dagger up dev.cue` again:

```shell
[✔] inputs.directories.app                                        0.1s
[✔] actions.build                                                 0.6s
[✔] actions.test                                                  0.6s
[+] actions.deploy                                                1.1s
#18 INFO: System: Ran is running on HTTP port 8020
#18 INFO: System: Listening on http://0.0.0.0:8020
```

Now that we have everything running locally, let us make a change and get a feel for our local CI/CD loop.
The quicker we can close this loop, the quicker we can learn what actually works.

In the todoapp dir, edit line `25` of `src/components/Form.js` and save the file.

I change this line to `What must be done today?` and run build, test & deploy again:

```shell
dagger up dev.cue

[✔] inputs.directories.app                                        7.5s
[✔] actions.build                                                94.7s
[✔] actions.test                                                 57.3s
[+] actions.deploy                                               96.1s
#18 INFO: System: Ran is running on HTTP port 8020
#18 INFO: System: Listening on http://0.0.0.0:8020
```

The total `94.7s` time is macOS specific, since the Linux alternative is more than 5x quicker.
Either way, this local build, test & deploy loop is likely to change the approach to iterating on changes.
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
With Docker Engine running, we are ready to download our example app and use its dev CI/CD pipeline:

```shell
git clone https://github.com/dagger/examples
cd examples/todoapp
```

With everything in place, we run the CI/CD pipeline locally:

```shell
dagger up dev.cue
```

With an empty cache, installing all dependencies, then building, testing & deploying this example app completes in just under 1 minute:

```shell
[✔] inputs.directories.app                                        0.3s
[✔] actions.test                                                 45.1s
[✔] actions.build                                                53.8s
[+] actions.deploy                                               57.5s
#18 INFO: System: Ran is running on HTTP port 8020
#18 INFO: System: Listening on http://0.0.0.0:8020
```

We can now access the application on [localhost:8020](http://localhost:8020) and get a preview of what the app would look like if the same thing ran in a CI environment.

While this is a good first step, it gets better when we run this again - the cache makes it quicker.
Type `^C` to exit the deployment, and run `dagger up dev.cue` again:

```shell
[✔] inputs.directories.app                                        0.1s
[✔] actions.build                                                 1.7s
[✔] actions.test                                                  1.8s
[+] actions.deploy                                                2.1s
#18 INFO: System: Ran is running on HTTP port 8020
#18 INFO: System: Listening on http://0.0.0.0:8020
```

Now that we have everything running locally, let us make a change and get a feel for our local CI/CD loop.
The quicker we can close this loop, the quicker we can learn what actually works.

In the todoapp dir, edit line `25` of `src/components/Form.js` and save the file.

I change this line to `What must be done today?` and run build, test & deploy again:

```shell
dagger up dev.cue

[✔] inputs.directories.app                                        0.1s
[✔] actions.build                                                24.7s
[✔] actions.test                                                 16.2s
[+] actions.deploy                                               17.1s
#18 INFO: System: Ran is running on HTTP port 8020
#18 INFO: System: Listening on http://0.0.0.0:8020
```

Being able to re-run a build, test & deploy loop locally in `17.1s` is likely to change the approach to iterating on changes.
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
Dagger makes this easy.
:::
