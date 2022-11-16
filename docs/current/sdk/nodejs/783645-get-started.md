---
slug: /sdk/nodejs/783645/get-started
---
import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Get Started with the Dagger Node.js SDK

## Introduction

This tutorial teaches you the basics of using Dagger in Node.js. You will learn how to:

- Install the Node.js SDK
- Create a Node.js CI tool that tests and builds a Node.js application for multiple Node.js versions using the Node.js SDK

## Requirements

This tutorial assumes that:

- You have a basic understanding of the TypeScript programming language. If not, learn the basics in a [TypeScript tutorial](https://www.typescriptlang.org/docs/handbook/typescript-from-scratch.html).
- You have a Node.js development environment with Node.js 16.x or later. If not, install [NodeJS](https://nodejs.org/en/download/).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

:::note
This tutorial creates, tests and builds a React application written in TypeScript. If you wish to use a different application, you must adjust the test and build commands in the code samples to match those needed by your application.
:::

## Step 1: Create a React application

Follow the steps below to create a sample React application.

1. Create a React application using the TypeScript template:

  ```shell
  npx create-react-app my-app --template typescript
  cd my-app
  ```

1. Install the TypeScript engine:

  ```shell
  npm install ts-node
  ```

1. Define the package type as a module:

  ```shell
  npm pkg set type=module
  ```

## Step 2: Install the Dagger Node.js SDK

:::note
The Dagger Node.js SDK requires [NodeJS 16.x or later](https://nodejs.org/en/download/).
:::

Install the Dagger Node.js SDK in your project using `npm` or `yarn`:

<Tabs>
<TabItem value="npm">

```shell
npm install @dagger.io/dagger
```

</TabItem>

<TabItem value="yarn">

```shell
yarn add @dagger.io/dagger
```

</TabItem>
</Tabs>

## Step 3: Create a Dagger client in Node.js

In your project directory, create a new file named `build.ts` and add the following code to it.

```typescript file=snippets/get-started/step3/build.ts
```

This Node.js stub imports the Dagger SDK and defines an asynchronous function. This function performs the following operations:

- It creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
- It uses the client's `container().from()` method to initialize a new container from a base image. In this example, the base image is the `node:16` image. This method returns a `Container` representing an OCI-compatible container image.
- It uses the `Container.exec()` method to define the command to be executed in the container - in this case, the command `node -v`, which returns the Node version string. The `exec()` method returns a revised `Container` with the results of command execution.
- It retrieves the output stream of the last executed command as a `File` object with the `Container.stdout()` method and uses the `File.contents()` methods to print the result to the console.

Run the Node.js CI tool by executing the command below from the project directory:

```shell
node --loader ts-node/esm ./build.ts
```

The tool outputs a string similar to the one below.

```shell
Hello from Dagger and Node v16.18.1
```

## Step 4: Test against a single Node.js version

Now that the basic structure of the CI tool is defined and functional, the next step is to flesh it out to actually test and build the React application.

Replace the `build.ts` file from the previous step with the version below (highlighted lines indicate changes):

```typescript file=snippets/get-started/step4/build.ts
```

The revised code now does the following:

- It creates a Dagger client with `connect()` as before.
- It uses the client's `host().workdir(["node_modules/"]).id()` method to obtain a reference to the current directory on the host. This reference is stored in the `source` variable. It also will ignore the `node_modules` directory on the host since we passed that in as an excluded directory.
- It uses the client's `container().from()` method to initialize a new container from a base image. This base image is the Node.js version to be tested against - the `node:16` image. This method returns a new `Container` object with the results.
- It uses the `Container.withMountedDirectory()` method to mount the host directory into the container at the `/src` mount point, and the `Container.withWorkdir()` method to set the working directory in the container. The revised `Container` is stored in the `runner` constant.
- It uses the `Container.exec()` method to define the command to run tests in the container - in this case, the command `npm test -- --watchAll=false`.
- It uses the `Container.exitCode()` method to execute the command and obtain the corresponding exit code. An exit code of `0` implies successful execution (all tests pass).
- It invokes the `Container.exec()` method again, this time to define the build command `npm run build` in the container.
- It obtains a reference to the `build/` directory in the container with the `Container.directory()` method. This method returns a `Directory` object.
- It writes the `build/` directory from the container to the host using the `Directory.export()` method.

:::tip
The `from()`, `withMountedDirectory()`, `withWorkdir()` and `exec()` methods all return a `Container`, making it easy to chain method calls together and create a pipeline that is intuitive to understand.
:::

Run the Node.js CI tool by executing the command below:

```shell
node --loader ts-node/esm ./build.ts
```

The tool tests and builds the application, logging the output of the test and build operations to the console as it works. At the end of the process, the built React application is available in a new `build` folder in the project directory, as shown below:

```shell
tree build
build
├── asset-manifest.json
├── favicon.ico
├── index.html
├── logo192.png
├── logo512.png
├── manifest.json
├── robots.txt
└── static
    ├── css
    │   ├── main.073c9b0a.css
    │   └── main.073c9b0a.css.map
    ├── js
    │   ├── 787.28cb0dcd.chunk.js
    │   ├── 787.28cb0dcd.chunk.js.map
    │   ├── main.f5e707f0.js
    │   ├── main.f5e707f0.js.LICENSE.txt
    │   └── main.f5e707f0.js.map
    └── media
        └── logo.6ce24c58023cc2f8fd88fe9d219db6c6.svg
```

## Step 5: Test against multiple Node.js versions

Now that the Node.js CI tool can test the application against a single Node.js version, the next step is to extend it for multiple Node.js versions.

Replace the `build.ts` file from the previous step with the version below (highlighted lines indicate changes):

```typescript file=snippets/get-started/step5/build.ts
```

This version of the CI tool has additional support for testing and building against multiple Node.js versions.

- It defines the test/build matrix, consisting of Node.js versions `12`, `14` and `16`.
- It iterates over this matrix, downloading a Node.js container image for each specified version and testing and building the source application against that version.
- It creates an output directory on the host named for each Node.js version so that the build outputs can be differentiated.

Run the Node.js CI tool by executing the command below:

```shell
node --loader ts-node/esm ./build.ts
```

The tool tests and builds the application against each version in sequence. At the end of the process, a built React application is available for each Node.js version in a `build-node-XX` folder in the project directory, as shown below:

```shell
tree -L 2 -d build-*
build-node-12
└── static
    ├── css
    ├── js
    └── media
build-node-14
└── static
    ├── css
    ├── js
    └── media
build-node-16
└── static
    ├── css
    ├── js
    └── media
```

## Conclusion

This tutorial introduced you to the Dagger Node.js SDK. It explained how to install the SDK and use it with a Node.js application. It also provided a working example of a Node.js CI tool powered by the SDK, demonstrating how to test an application against multiple Node.js versions in parallel.

Use the SDK Reference to learn more about the Dagger Node.js SDK.
