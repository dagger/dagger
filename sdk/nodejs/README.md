# Dagger NodeJS SDK

A client package for running [Dagger](https://dagger.io/) pipelines.

## What is the Dagger NodeJS SDK?

The Dagger NodeJS SDK contains everything you need to develop CI/CD pipelines in Typescript of Javascript, and run them on any OCI-compatible container runtime.

## Install

```shell
npm install @dagger.io/dagger --save-dev
```

## Local development

You may want to work on the NodeSDK and test it directly on a local node project. 

1. Create a new Node project

:warning: Skip this step if you already have a node project

```shell
mkdir my-test-ts-project

# Init project (you may use yarn or pnpm)
npm init -y

# Add typescript
npm install typescript --save-dev

# Init typescript project
npx tsc --init
```

2. Update project settings

Dagger exports its SDK using type module so you will need to also update
your `package.json` to the same type.

Add or update the field `type` in your `package.json`

```json
"type": "module"
```

You must also update your `tsconfig.json` to use `NodeNext` as `module`.

```json
"module": "NodeNext"
```

3. Import Dagger local module

Go to the Dagger nodejs SDK directory and do the following :

```shell
cd path/to/dagger/sdk/nodejs
yarn link
```

Go back to the root directory of your local project to link the nodejs sdk.

```shell
yarn link @dagger.io/dagger  
```

4. Use Dagger

You can now import the local Dagger NodeSDK as if you were using the official one.

```ts
import { connect } from '@dagger.io/dagger"
```

:bulb: Modifications applied to the local Dagger NodeSDK will be applied to your
project one you recompile the module using `yarn build`.

## Documentation

Please [visit our documentation](https://docs.dagger.io/sdk/nodejs/835948/install) for a full list of commands and examples.