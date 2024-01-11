# Dagger TypeScript SDK

A client package for running [Dagger](https://dagger.io/) pipelines.

## What is the Dagger TypeScript SDK?

The Dagger TypeScript SDK contains everything you need to develop CI/CD pipelines in TypeScript or Javascript, and run them on any OCI-compatible container runtime.

## Install

```shell
npm install @dagger.io/dagger --save-dev
```

## Local development

You may want to work on the TypeScript SDK and test it directly on a local node project.

### 1. Create a new Node project

:warning: Skip this step if you already have a node project

```shell
mkdir my-test-ts-project

# Init project (you may use yarn or pnpm)
npm init -y

# Add typescript
npm install typescript ts-node --save-dev

# Init typescript project
npx tsc --init
```

### 2. Update project settings

Dagger exports its SDK using type module so you will need to also update
your `package.json` to the same type.

Add or update the field `type` in your `package.json` from your project root directory:

```shell
npm pkg set type=module
```

You must also update your `tsconfig.json` to use `NodeNext` as `module`.

```json
"module": "NodeNext"
```

### 3. Symlink Dagger local module

Go to the Dagger TypeScript SDK directory and do the following :

```shell
cd path/to/dagger/sdk/typescript # go into the package directory
npm link # creates global link
```

Go back to the root directory of your local project to link the TypeScript SDK.

```shell
cd path/to/my_app # go into your project directory.
npm link @dagger.io/dagger # link install the package
```

:bulb: Any changes to `path/to/dagger/sdk/typescript` will be reflected in `path/to/my_app/node_modules/@dagger.io/dagger`.

### 4. Make your contribution

While making SDK code modification you should `watch` the input files:

```shell
cd path/to/dagger/sdk/typescript # go into the package directory
yarn watch # Recompile the code when input files are modified
```

You can now import the local Dagger TypeScript SDK as if you were using the official one.

```ts
import { connect } from "@dagger.io/dagger"
```

## Documentation

Please [visit our documentation](https://docs.dagger.io/sdk/nodejs/835948/install) for a full list of commands and examples.
