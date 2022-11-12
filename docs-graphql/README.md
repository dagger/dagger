# GraphQL API Documentation

## Context

We chose to use `spectaql` as a quick way to generate a static HTML webpage for our GraphQL API reference, because it generates documentation based on a GraphQL schema.
For generating documentation based on a schema, `spectaql` uses [Handlebars](https://handlebarsjs.com/) and [Microfiber](https://www.npmjs.com/package/microfiber).  

In order to tailor the documentation to our needs, we have to override the current styling and data produced from the schema.

## Examples Generation

The examples are rendered with `spectaql` with the use of [helpers](https://github.com/anvilco/spectaql/tree/1c125e0c735f354337b18c4bd773759c4e65075b/src/themes/default/helpers) that live in the default theme. Helpers are a core concept in Handlebars.  

The examples live in the [`./data/examples`](./data/examples/) folder and are read at the time of generation with a script that lives in [`./custom-theme/data/index.js`](`./custom-theme/data/index.js`).  

The script does not fail if there is a missing example for a given query, but it outputs the results to the console with a warning.  