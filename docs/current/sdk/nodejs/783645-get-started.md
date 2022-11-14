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

- You have a basic understanding of the TypeScript programming language. If not, learn the basics in a [TypeScript tutorial](https://www.typescriptlang.org/docs/handbook/typescript-from-scratch.html).
- You have a NodeJS development environment with NodeJS 16.x or later. If not, install [NodeJS](https://nodejs.org/en/download/).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

:::note
This tutorial builds and tests a React application. If you don't have a React application already, clone an existing application with a well-defined test suite before proceeding. Alternatively, create a simple React application (which comes with a built-in test) as below:

```shell
npx create-react-app myapp
```

The code samples in this tutorial are based on the above React application. If using a different project, adjust the code samples accordingly.
:::

## Step 1: Install the Dagger NodeJS SDK

:::note
The Dagger NodeJS SDK requires [NodeJS 16 or later](https://nodejs.org/en/download/).
:::

Install the Dagger NodeJS SDK in your project using `npm`:

```shell
npm install @dagger.io/dagger
```

Alternatively, install using `yarn`:

```shell
yarn add @dagger.io/dagger
```

## Step 2: Create a Dagger client in NodeJS

Create a new file named `build.ts` and add the following code to it.

```typescript file=snippets/get-started/step2/build.ts
```

```typescript
import Client, { connect } from "@dagger.io/dagger"

 // initialize Dagger client
connect(async (client: Client) => {

  // get Node image
  // get Node version
  let node = await client
    .container()
    .from({ address: `node:16` })
    .exec({ args: ["node", "-v"] })

  // execute
  let version = await node
    .stdout
    .contents()

  // print output
  console.log("Hello from Dagger and Node " + version)
});
```

This NodeJS stub imports the Dagger SDK and defines an asynchronous function. This function performs the following operations:

- It creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
- It uses the client's `container().from()` method to initialize a new container from a base image. In this example, the base image is the `node:16` image. This method returns a `Container` representing an OCI-compatible container image.
- It uses the `Container.exec()` method to define the command to be executed in the container - in this case, the command `node -v`, which returns the Node version string. The `exec()` method returns a revised `Container` with the results of command execution.
- It retrieves the output stream of the last executed command with the `Container.stdout()` method and prints its contents.

Run the Python CI tool by executing the command below from the project directory:

```shell
python test.py
```

The tool outputs a string similar to the one below.

```shell
Hello from Dagger and Python 3.10.8
```



## Step 2: Code snippet

```typescript

```
