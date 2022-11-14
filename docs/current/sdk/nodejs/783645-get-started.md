---
slug: /sdk/nodejs/783645/get-started
---

# Get Started with the Dagger NodeJS SDK

## Introduction

This tutorial teaches you the basics of using Dagger in NodeJS. You will learn how to:

- Install the NodeJS SDK
- Create a NodeJS CI tool to ...

## Requirements

This tutorial assumes that:

- You have a basic understanding of the Typescript or Javascript programming languages. If not, [read the Typescript tutorials](https://www.typescriptlang.org/docs/).
- You have a NodeJS development environment with NodeJS 16 or later. If not, install [NodeJS](https://nodejs.org/en/download/).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

:::note
This tutorial build, test and deploy a React application.
If you don't have a React application already, clone an existing React project with a well-defined test suite before proceeding.
A good example is the [Getting started with NodeJS SDK](https://github.com/slumbering/gettingstarted-nodejs-sdk) repo, which you can clone as below:

```shell
git clone https://github.com/slumbering/gettingstarted-nodejs-sdk.git
```

The code samples in this tutorial are based on the above React app project. If using a different project, adjust the code samples accordingly.
:::

## Step 1: Install the Dagger NodeJS SDK

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

:::note
The Dagger NodeJS SDK requires [NodeJS 16 or later](https://nodejs.org/en/download/).
:::

Install the Dagger NodeJS SDK in your project's using `npm or yarn`:

<Tabs
defaultValue="npm"
values={[
{label: 'Npm', value: 'npm'},
{label: 'Yarn', value: 'yarn'},
]}>

<TabItem value="npm">

```shell
cd gettingstarted-nodejs-sdk/

# install every packages
npm install

# install dagger NodeJS SDK
npm install @dagger.io/dagger
```

</TabItem>

<TabItem value="yarn">

```shell
cd gettingstarted-nodejs-sdk/

# install every packages
yarn

# install dagger NodeJS SDK
yarn add @dagger.io/dagger
```

</TabItem>

</Tabs>

## Step 2: Code snippet

```typescript
import Client, { connect } from "@dagger.io/dagger"

 // initialize Dagger client
connect(async (client: Client) => {

  // Set Node versions to test
  const nodeVersions = ["12", "14", "16"]

  // get reference to the local project
  const source = await client.host().workdir().id();

  for(const nodeVersion of nodeVersions) {

    // get Node image
    const node = await client
      .container()
      .from({ address: `node:${nodeVersion}` })
      .id()

    // mount cloned repository into node image
    const runTest = client
      .container({ id: node.id })
      .withMountedDirectory({ path: "/src", source: source.id })
      .withWorkdir({ path: "/src" })

    // Run test for earch node version
    await runTest
      .exec({ args: ["npm", "test", "--", "--watchAll=false"] })
      .exitCode()

    // Run build for each node version 
    // and write the contents of the directory on the host
    await client
      .container({ id: node.id })
      .withMountedDirectory({ path: "/src", source: source.id })
      .withWorkdir({ path: "/src" })
      .exec({ args: ["npm", "run", "build"] })
      .directory({path: "build/"})
      .export({path: `./build-node-${nodeVersion}`})
  }
});
```

