# Website

This website is built using [Docusaurus 2](https://docusaurus.io/), a modern static website generator.

## Installation

```console
yarn install
```

## Local Development

```console
yarn start
```

This command starts a local development server and opens up a browser window. Most changes are reflected live without having to restart the server.

## Build

```console
yarn build
```

This command generates static content into the `build` directory and can be served using any static contents hosting service.


## Build API reference

### Installation

```console
npm i @edno/docusaurus2-graphql-doc-generator graphql
npm i @graphql-tools/url-loader
```

### Document generation

```console
cloak dev &
npx docusaurus graphql-to-doc
```