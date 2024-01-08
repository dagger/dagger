# Documentation Style Guide

## Files

- Filenames for standalone articles (not list or index pages) should be prefixed with random 6-digit identifier generated via `new.sh` script
- Use `.mdx` for all files
- URLs for SDK-specific pages should be in the form `/sdk/[language]/[number]/[label]`
- URLs for API- or CLI-specific pages should be in the form `/[api|cli]/[number]/[label]`

## Page titles

- Page titles should be in Word Case
- For guides and tutorials, start each title with a verb e.g. `Create Pipeline...`, `Deploy App...`

## Page sections

- Section headings should be in Sentence case

## Code

- Example code should be stored in the `sdk/snippets` or `guides/snippets` subdirectory
  - Subdirectory name = docs filename that embeds its snippets
  - Separate each code snippet further into its own subdirectory if required so it can be run standalone
- By default, provide code snippets for all available SDK languages, with each code snippet in a separate tab. Here is an example:

  ```html
  <Tabs groupId="language">
  <TabItem value="Go">
  ...
  </TabItem>
  <TabItem value="TypeScript">
  ...
  </TabItem>
  <TabItem value="Python">
  ...
  </TabItem>
  </Tabs>
  ```

- Use API Playground embeds where possible (see "API Playground embeds" section below).
- In inline text, when referring to an SDK method, instead of explicitly specifying the name for each language method (they have minor differences), use the GraphQL API type and field instead e.g `Container.withServiceBinding` instead of `Container.WithServiceBinding (Go), Container.with_service_binding (Python) and Container.withServiceBinding (TypeScript)`.
  - Always `Capitalize` types, and always `lowerCamelCase` fields, to match GraphQL syntax as the common compromise.
  - Omit the `()` since a lot of the time it's either not necessary, or implies no args are needed, and sometimes you just want to refer to a method call and ignore its required args e.g. `Container.asService`.

## Images

- Images must be PNG format with a minimum width of 679 px.

## Text

- Avoid using `you`, `we`, `our` and other personal pronouns. Alternatives are (e.g. instead of `this is where you will deploy the application`):
  - Rewrite the sentence using passive voice e.g. `this is where the application will be deployed`
  - Rewrite the sentence to personify the subject e.g. `the application will be deployed here`
  - Rewrite the sentence using active voice and a verb e.g. `deploy the application to...`

## API Playground embeds

An API Playground embed allows users to run code snippets interactively in their browser.

NOTE: An API Playground embed is generated from a source file, so you must still create and maintain a code snippet file for each embed. This also ensures that the code snippet is automatically tested by the CI. Currently supported file extensions are `.go`, `.ts` (Quickstart only), `.js`, `.mjs` and `.py`.

Follow the steps below to create an API Playground embed:

1. Prepare and save the code snippet you wish to use in a file (see the "Code" section above).
1. Browse to [https://play.dagger.cloud/playground](https://play.dagger.cloud/playground) and log in.
1. Open your browser's network inspector (usually found under the "Browser Tools" or "Web Developer Tools" menu).
1. Click the "Play" button to run the example query in the API Playground. Monitor the network inspector console for a POST request to `https://api.dagger.cloud/playgrounds`.
1. Inspect the POST request headers and copy the bearer token from the `Authorization:` header. This token will typically begin with `ey`.
1. Create a local environment variable with the token:

    ```shell
    export TOKEN="YOUR-TOKEN"
    ```

1. Create the API Playground embed. The output of the command is a URL in the format `https://play.dagger.cloud/embed/XYZ`. Note that the command differs depending on whether the embed is for use in the Quickstart or elsewhere, as the Quickstart has special requirements.

    ```shell
    cd docs
    # for embeds apart from quickstart embeds
    TOKEN=$TOKEN ./create_embed.sh YOUR-SNIPPET-FILE
    # for quickstart embeds
    TOKEN=$TOKEN ./create_embed_qs.sh YOUR-SNIPPET-FILE
    ```

1. Add the embed to your document using the format below:

    ```html
    <iframe class="embed" src="YOUR-EMBED-LINK"></iframe>
    ```

## Cookbook recipes

The Dagger Cookbook is a collection of code listings or "recipes" for common tasks. It is a thin layer of organization over code snippets sourced from external files.

### Recipe categories

The Cookbook is organized into categories, each of which has two or more recipes. New categories may be added at will; however, ideally a category should contain at least two recipes.

### Recipes

Each recipe requires only:

- a level-3 heading, which must begin with a verb (required);
- a one-sentence description of what the recipe does and additional notes for any placeholder replacements to be performed when using the recipe (required);
- the code itself, which must be presented for each SDK (required);
- a "learn more" link (optional).

### Code listings

Code listing can come from two sources:

- Snippets used in an existing guide (usually stored in `./guides/snippets/GUIDE/FILE`)
- Snippets created specifically for the cookbook (usually stored in `./cookbook/snippets/RECIPE/FILE`)

Code listings must be presented for each language SDK unless not relevant/not technically feasible for that language (e.g. a recipe for "using a magefile" would only be relevant for Go).

Code listings must be presented in a tabbed interface with the order of tabs set to `Go`, `TypeScript` and `Python`.
