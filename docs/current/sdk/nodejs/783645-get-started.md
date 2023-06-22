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

- You have a Node.js development environment with Node.js 16.x or later. If not, install [NodeJS](https://nodejs.org/en/download/).
- You have a Node.js application developed in either JavaScript or TypeScript. If not, follow the steps in Appendix A to [create an example React application in TypeScript](#appendix-a-create-a-react-application).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Step 1: Install the Dagger Node.js SDK

:::note
The Dagger Node.js SDK requires [NodeJS 16.x or later](https://nodejs.org/en/download/).
:::

Install the Dagger Node.js SDK in your project using `npm` or `yarn`:

<Tabs>
<TabItem value="npm">

```shell
npm install @dagger.io/dagger@latest --save-dev
```

</TabItem>

<TabItem value="yarn">

```shell
yarn add @dagger.io/dagger --dev
```

</TabItem>
</Tabs>

## Step 2: Create a Dagger client in Node.js

<Tabs groupId="typescript-javascript">
  <TabItem value="ts" label="TypeScript">

Install the TypeScript engine (if not already present):

```shell
npm install ts-node typescript
```

Create a TypeScript configuration file (if not already present):

```shell
npx tsc --init --module esnext --moduleResolution nodenext
```

In your project directory, create a new file named `build.mts` and add the following code to it.

```typescript file=snippets/get-started/step2/build.mts
```

  </TabItem>
  <TabItem value="js-esm" label="JavaScript (ESM)">

In your project directory, create a new file named `build.mjs` and add the following code to it.

```typescript file=snippets/get-started/step2/build.mjs
```

  </TabItem>
  <TabItem value="js-cjs" label="JavaScript (CommonJS)">

In your project directory, create a new file named `build.js` and add the following code to it.

```typescript file=snippets/get-started/step2/build.js
```

  </TabItem>
</Tabs>

This Node.js stub imports the Dagger SDK and defines an asynchronous function. This function performs the following operations:

- It creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
- It uses the client's `container().from()` method to initialize a new container from a base image. In this example, the base image is the `node:16` image. This method returns a `Container` representing an OCI-compatible container image.
- It uses the `Container.withExec()` method to define the command to be executed in the container - in this case, the command `node -v`, which returns the Node version string. The `withExec()` method returns a revised `Container` with the results of command execution.
- It retrieves the output stream of the last executed with the `Container.stdout()` method and prints the result to the console.

Run the Node.js CI tool by executing the command below from the project directory:

<Tabs groupId="typescript-javascript">
  <TabItem value="ts" label="TypeScript">

```shell
node --loader ts-node/esm ./build.mts
```

  </TabItem>
  <TabItem value="js-esm" label="JavaScript (ESM)">

```shell
node ./build.mjs
```

  </TabItem>
  <TabItem value="js-cjs" label="JavaScript (CommonJS)">

```shell
node ./build.js
```

  </TabItem>
</Tabs>

The tool outputs a string similar to the one below.

```shell
Hello from Dagger and Node v16.18.1
```

## Step 3: Test against a single Node.js version

Now that the basic structure of the CI tool is defined and functional, the next step is to flesh it out to actually test and build the application.

<Tabs groupId="typescript-javascript">
  <TabItem value="ts" label="TypeScript">

Replace the `build.mts` file from the previous step with the version below (highlighted lines indicate changes):

```typescript file=snippets/get-started/step3/build.mts
```

  </TabItem>
  <TabItem value="js-esm" label="JavaScript (ESM)">

Replace the `build.mjs` file from the previous step with the version below (highlighted lines indicate changes):

```typescript file=snippets/get-started/step3/build.mjs
```

  </TabItem>
  <TabItem value="js-cjs" label="JavaScript (CommonJS)">

Replace the `build.js` file from the previous step with the version below (highlighted lines indicate changes):

```typescript file=snippets/get-started/step3/build.js
```

  </TabItem>
</Tabs>

The revised code now does the following:

- It creates a Dagger client with `connect()` as before.
- It uses the client's `host().directory(".", ["node_modules/"])` method to obtain a reference to the current directory on the host. This reference is stored in the `source` variable. It also will ignore the `node_modules` directory on the host since we passed that in as an excluded directory.
- It uses the client's `container().from()` method to initialize a new container from a base image. This base image is the Node.js version to be tested against - the `node:16` image. This method returns a new `Container` object with the results.
- It uses the `Container.withDirectory()` method to mount the host directory into the container at the `/src` mount point, and the `Container.withWorkdir()` method to set the working directory in the container. The revised `Container` is stored in the `runner` constant.
- It uses the `Container.withExec()` method to define the command to run tests in the container - in this case, the command `npm test -- --watchAll=false`.
- It uses the `Container.sync()` method to execute the command.
- It invokes the `Container.withExec()` method again, this time to define the build command `npm run build` in the container.
- It obtains a reference to the `build/` directory in the container with the `Container.directory()` method. This method returns a `Directory` object.
- It writes the `build/` directory from the container to the host using the `Directory.export()` method.

:::tip
The `from()`, `withDirectory()`, `withWorkdir()` and `withExec()` methods all return a `Container`, making it easy to chain method calls together and create a pipeline that is intuitive to understand.
:::

Run the Node.js CI tool by executing the command below:

<Tabs groupId="typescript-javascript">
  <TabItem value="ts" label="TypeScript">

```shell
node --loader ts-node/esm ./build.mts
```

  </TabItem>
  <TabItem value="js-esm" label="JavaScript (ESM)">

```shell
node ./build.mjs
```

  </TabItem>
  <TabItem value="js-cjs" label="JavaScript (CommonJS)">

```shell
node ./build.js
```

  </TabItem>
</Tabs>

The tool tests and builds the application, logging the output of the test and build operations to the console as it works. At the end of the process, the built application is available in a new `build` folder in the project directory. Here is an example of the output when building a React application:

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

## Step 4: Test against multiple Node.js versions

Now that the Node.js CI tool can test the application against a single Node.js version, the next step is to extend it for multiple Node.js versions.

<Tabs groupId="typescript-javascript">
  <TabItem value="ts" label="TypeScript">

Replace the `build.mts` file from the previous step with the version below (highlighted lines indicate changes):

```typescript file=snippets/get-started/step4/build.mts
```

  </TabItem>
  <TabItem value="js-esm" label="JavaScript (ESM)">

Replace the `build.mjs` file from the previous step with the version below (highlighted lines indicate changes):

```typescript file=snippets/get-started/step4/build.mjs
```

  </TabItem>
  <TabItem value="js-cjs" label="JavaScript (CommonJS)">

Replace the `build.js` file from the previous step with the version below (highlighted lines indicate changes):

```typescript file=snippets/get-started/step4/build.js
```

  </TabItem>
</Tabs>
This version of the CI tool has additional support for testing and building against multiple Node.js versions.

- It defines the test/build matrix, consisting of Node.js versions `12`, `14` and `16`.
- It iterates over this matrix, downloading a Node.js container image for each specified version and testing and building the source application against that version.
- It creates an output directory on the host named for each Node.js version so that the build outputs can be differentiated.

Run the Node.js CI tool by executing the command below:

<Tabs groupId="typescript-javascript">
  <TabItem value="ts" label="TypeScript">

```shell
node --loader ts-node/esm ./build.mts
```

  </TabItem>

  <TabItem value="js-esm" label="JavaScript (ESM)">

```shell
node ./build.mjs
```

  </TabItem>
  <TabItem value="js-cjs" label="JavaScript (CommonJS)">

```shell
node ./build.js
```

  </TabItem>
</Tabs>

The tool tests and builds the application against each version in sequence. At the end of the process, a built application is available for each Node.js version in a `build-node-XX` folder in the project directory, as shown below:

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

Use the [SDK Reference](./reference/modules.md) to learn more about the Dagger Node.js SDK.

## Appendix A: Create a React application

Create a React application using the TypeScript template:

```shell
npx create-react-app my-app --template typescript
cd my-app
```
