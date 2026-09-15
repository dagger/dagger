# Documentation Style Guide

## Files

- Use `.mdx` for all files

## Page titles

- Page titles should be in Word Case
- For guides and tutorials, start each title with a verb e.g. `Create Pipeline...`, `Deploy App...`

## Page sections

- Section headings should be in Sentence case

## Code

- Example code should be stored in a `snippets/XXX` subdirectory
  - Separate each code snippet further into a language subdirectory so it can be run standalone
- By default, provide code snippets for all available SDK languages, with each code snippet in a separate tab. The order of tabs should always be `Go`, `Python` and `TypeScript`. Here is an example:

  ```html
  <Tabs groupId="language" queryString="sdk">
  <TabItem value="go" label="Go">
  ...
  </TabItem>
  <TabItem value="python" label="Python">
  ...
  </TabItem>
  <TabItem value="typescript" label="TypeScript">
  ...
  </TabItem>
  </Tabs>
  ```

- Always capitalize and use code font for Dagger core types such as `Container`, `Secret`, etc
- Omit the `()` for readability since a lot of the time it's either not necessary, or implies no args are needed, and sometimes you just want to refer to a method call and ignore its required args e.g. `Container.asService`
- Dagger Functions and arguments in code listings should be documented inline "wherever possible", except for Cookbook recipes where this is "mandatory" since these are intended to be best-practice examples. This inline documentation includes at minimum
  - a one-line comment for the function
  - a description for each argument apart from `ctx` (Go) and `self` (Python)
- The minimal set of files to be included for a code listing are
  - `dagger.json`
  - `.gitignore`
  - `.gitattributes` (optional)
  - (Go) `main.go`, `go.mod` and `go.sum`

## Images

- Images must be PNG format with a minimum width of 679 px

## Text

- Avoid using `you`, `we`, `our` and other personal pronouns, except in the quickstart which is intended  as a companion journey. Alternatives are (e.g. instead of `this is where you will deploy the application`):
  - Rewrite the sentence using passive voice e.g. `this is where the application will be deployed`
  - Rewrite the sentence to personify the subject e.g. `the application will be deployed here`
  - Rewrite the sentence using active voice and a verb e.g. `deploy the application to...`

## Lists

- When writing lists, use a hyphen `-` for unordered lists and a number followed by a period `1.` for ordered lists
- Lists should not end with a period
- List lead-ins or labels followed by a colon (`:`) should be in bold text e.g. `**State and duration**: Get visual cues for cached and pending states, and see exactly how long each step of your workflow takes.`

## Formatting styles

- Body text should not use bold, italic or other formatting styles except in the following cases:
  - List lead-ins or labels should use bold text (see `Lists` section)
  - User interface labels should use bold text e.g. `Click the **Start** button.`

## Cookbook

### Recipe categories

The Cookbook is organized into categories, each of which has two or more recipes. New categories may be added at will; however, ideally a category should contain at least two recipes.

### Recipes

Each recipe requires only:

- a level-3 heading, which must begin with a verb (required)
- a one-sentence description of what the recipe does and additional notes for any placeholder replacements to be performed when using the recipe (required)
- the code itself, which must be presented for each SDK (required)
- one or more usage examples of `dagger call`, in an "Example" level-4 heading (required)
- a "learn more" link (optional)

### Code listings

- Code listings can come from two sources:
  - Snippets used in an existing guide
  - Snippets created specifically for the cookbook (usually stored in `./cookbook/snippets/RECIPE/LANGUAGE/FILE`)
- Code listings must be presented for each language SDK unless not relevant/not technically feasible for that language (e.g. a recipe for "using a magefile" would only be relevant for Go)
- Code listings must be presented in a tabbed interface with the order of tabs set to `Go`, `Python` and `TypeScript`

### Screen recordings

Some screen recordings can be auto-generated with the `docs/recorder` module

- Generate recordings for some feature pages:

  ```shell
  dagger call generate-feature-recordings --base=../current_docs/features/snippets --github-token=<plaintext-token> export --path=/tmp/out
  ```

- Generate recordings manually for other feature pages:

    ```shell
    dagger logout
    export PS1="$ " >> ~/.bashrc
    # run each command once to warm the cache before recording
    asciinema rec --overwrite --cols=80 --rows=24  ~/images/debug-breakpoints.asc
    asciinema rec --overwrite --cols=80 --rows=24  ~/images/debug-interactive.asc
    asciinema rec --overwrite --cols=80 --rows=24  ~/images/service-container.asc
    asciinema rec --append --cols=80 --rows=24  ~/images/service-container.asc # in separate console
    asciinema rec --overwrite --cols=80 --rows=24  ~/images/service-host.asc
    # manually edit all .asc files to remove closing `$ exit`
    # manually edit `service-container.asc` file to insert line break `\n` between terminal outputs
    cd ~/images
    docker run --rm -it -u $(id -u):$(id -g) -v $PWD:/data agg <file>.asc <file>.gif
    ```

- Generate recordings for some quickstart pages:

  ```shell
  dagger call generate-quickstart-recordings --base=../current_docs/ci/quickstart/snippets export --path=/tmp/out
  ```
