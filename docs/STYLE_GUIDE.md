# Documentation Style Guide

## Files

- Filenames should be prefixed with random 6-digit identifier generated via `new.sh` script
- URLs for SDK-specific pages should be in the form `/sdk/[language]/[number]/[label]`
- URLs for API- or CLI-specific pages should be in the form `/[api|cli]/[number]/[label]`
- URLs for non-SDK pages should be in the form `/[number]/[label]`

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
  <TabItem value="Node.js">
  ...
  </TabItem>
  <TabItem value="Python">
  ...
  </TabItem>
  </Tabs>
  ```

- Use API Playground embeds where possible (see "API Playground embeds" section below).

## Text

- Avoid using `you`, `we`, `our` and other personal pronouns. Alternatives are (e.g. instead of `this is where you will deploy the application`):
  - Rewrite the sentence using passive voice e.g. `this is where the application will be deployed`
  - Rewrite the sentence to personify the subject e.g. `the application will be deployed here`
  - Rewrite the sentence using active voice and a verb e.g. `deploy the application to...`

## API Playground embeds

An API Playground embed allows users to run code snippets interactively in their browser.

NOTE: An API Playground embed is generated from a source file, so you must still create and maintain a code snippet file for each embed. This also ensures that the code snippet is automatically tested by the CI. Currently supported file extensions are `.go`, `.ts`, `.js`, `.python`.

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

8. Add the embed to your document using the format below:

    ```html
    <iframe class="embed" src="YOUR-EMBED-LINK"></iframe>
    ```
