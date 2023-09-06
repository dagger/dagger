---
slug: /faq
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# FAQ

## General

### How do I install Dagger?

Refer to the documentation for information on how to install the [Dagger CLI](./cli/465058-install.md) and the Dagger [Go](./sdk/go/371491-install.md), [Node.js](./sdk/nodejs/835948-install.md) and [Python](./sdk/python/866944-install.md) SDKs.

### How do I update Dagger?

:::tip
[Learn more about compatibility between the Dagger Engine, the Dagger CLI and Dagger SDKs](#what-compatibility-is-there-between-the-dagger-engine-dagger-sdks-and-dagger-cli-versions).
:::

#### CLI

To install a Dagger CLI that matches your OS & architecture, run the following next to the `bin` directory where `dagger` is currently installed, e.g. `/usr/local`:

```shell
curl -L https://dl.dagger.io/dagger/install.sh | sh
```

The above will create `./bin/dagger`.

Homebrew users can alternatively use the following commands:

```shell
brew update
brew upgrade dagger
```

#### SDKs

To update a Dagger SDK, follow the same procedure that you would follow to update any other SDK package in your chosen development environment. For example:

- Go SDK

  ```shell
  go get -u dagger.io/dagger
  ```

- Node.js SDK

  <Tabs>
  <TabItem value="npm">

  ```shell
  npm install @dagger.io/dagger@latest --save-dev
  ```

  </TabItem>
  <TabItem value="yarn">

  ```shell
  yarn upgrade --latest @dagger.io/dagger --dev
  ```

  </TabItem>
  </Tabs>

- Python SDK

  <Tabs>
  <TabItem value="PyPI">

  ```shell
  pip install -U dagger-io
  ```

  </TabItem>
  <TabItem value="Conda">

  ```shell
  conda update dagger-io
  ```

  </TabItem>
  </Tabs>

### What compatibility is there between the Dagger Engine, Dagger SDKs and Dagger CLI versions?

- The Dagger CLI is released in tandem with the Dagger Engine and thus shares a version number with it.
- Dagger SDKs automatically provision a Dagger Engine at a compatible version.

Each release notes the compatible Dagger Engine version in its release notes. If running from the CLI, or providing a CLI for the SDK to use via the `_EXPERIMENTAL_DAGGER_CLI_BIN` variable, check the release notes of the SDK, which indicate the required CLI and Engine versions.

The image below shows an example of the compatibility information available in the release notes:

![Release information](/img/current/faq/release-notes.png)

### How do I uninstall Dagger?

Follow these steps:

1. To uninstall a Dagger SDK, follow the same procedure that you would follow to uninstall any other SDK package in your chosen development environment.

1. Remove the Dagger CLI using the following command:

  ```shell
  sudo rm /usr/local/bin/dagger
  ```

  Homebrew users can alternatively use the following command:

  ```shell
  brew uninstall dagger
  ```

1. Remove the Dagger container using the following commands:

  ```shell
  docker rm --force --volumes "$(docker ps --quiet --filter='name=^dagger-engine-')"
  ```

1. Remove the `dagger` sub-directory of your local cache and configuration directories (`$XDG_CACHE_HOME` and `$XDG_CONFIG_HOME` on Linux or the equivalent for other platforms):

  <Tabs>
  <TabItem value="macOS">

  ```shell
  rm -rf ~/Library/Caches/dagger
  rm -rf ~/Library/Application\ Support/dagger
  ```

  </TabItem>
  <TabItem value="Linux">

  ```shell
  rm -rf ~/.cache/dagger
  rm -rf ~/.config/dagger
  ```

  </TabItem>
  </Tabs>

  :::note
  The paths listed above are defaults and may require adjustment for your specific environment. The third-party packages Dagger uses to determine these paths are listed below:
    - Go (SDK and CLI): [https://github.com/adrg/xdg](https://github.com/adrg/xdg)
    - Node.js: [https://github.com/sindresorhus/env-paths](https://github.com/sindresorhus/env-paths)
    - Python: [https://github.com/platformdirs/platformdirs](https://github.com/platformdirs/platformdirs)
  :::

### I am stuck. How can I get help?

Join us on [Discord](https://discord.com/invite/dagger-io), and ask your question in our [help forum](https://discord.com/channels/707636530424053791/1030538312508776540). Our team will be happy to help you there!

## Dagger SDKs

### What language SDKs are available for Dagger?

We currently offer technical previews of a [Go SDK](/sdk/go), a [Node.js SDK](/sdk/nodejs) and a [Python SDK](/sdk/python). Waiting for your favorite language to be supported? [Let us know which one](https://airtable.com/shrzABOn1wCk5yBF4), and we'll notify you when it's ready.

### How do I log in to a container registry using a Dagger SDK?

There are two options available:

1. Use the [`Container.withRegistryAuth()`](https://docs.dagger.io/api/reference/#Container-withRegistryAuth) GraphQL API method. A native equivalent of this method is available in each Dagger SDK ([example](./guides/723462-use-secrets.md#use-secrets-with-dagger-sdk-methods)).
1. Dagger SDKs can use your existing Docker credentials without requiring separate authentication. Simply execute `docker login` against your container registry on the host where your Dagger pipelines are running.

## Dagger API

### What API query language does Dagger use?

Dagger uses GraphQL as its low-level language-agnostic API query language.

### Do I need to know GraphQL to use Dagger?

No. You only need to know one of Dagger's supported SDKs languages to use Dagger. The translation to underlying GraphQL API calls is handled internally by the Dagger SDK of your choice.

### There's no SDK for &lt;language&gt; yet. Can I still use Dagger?

Yes. It's possible to use the Dagger GraphQL API from any language that [supports GraphQL](https://graphql.org/code/) ([example](./api/254103-build-custom-client.md)) or from the [Dagger CLI](./cli/index.md).
