# Documentation Style Guide

## Files

- Use `.mdx` for all files.

## Page titles

- Page titles should be in Word Case.
- For guides and tutorials, start each title with a verb e.g. `Create Pipeline...`, `Deploy App...`.

## Page sections

- Section headings should be in Sentence case.

## Code

- Example code should be stored in a `snippets/XXX` subdirectory.
  - Separate each code snippet further into a language subdirectory so it can be run standalone.
- By default, provide code snippets for all available SDK languages, with each code snippet in a separate tab. The order of tabs should always be `Go`, `Python` and `TypeScript`. Here is an example:

  ```html
  <Tabs groupId="language">
  <TabItem value="Go">
  ...
  </TabItem>
  <TabItem value="Python">
  ...
  </TabItem>
  <TabItem value="TypeScript">
  ...
  </TabItem>
  </Tabs>
  ```

- Always capitalize Dagger core types such as `Container`, `Secret`, etc.
- Omit the `()` for readability since a lot of the time it's either not necessary, or implies no args are needed, and sometimes you just want to refer to a method call and ignore its required args e.g. `Container.asService`.
- Dagger Functions and arguments in code listings should be documented inline "wherever possible", except for Cookbook recipes where this is "mandatory" since these are intended to be best-practice examples. This inline documentation includes at minimum
  - a one-line comment for the function;
  - a description for each argument apart from `ctx` (Go) and `self` (Python).
- The minimal set of files to be included for a code listings are:
  - `dagger.json`
  - `.gitignore`
  - `.gitattributes` (optional)
  - (Go) `main.go`, `go.mod` and `go.sum`

## Images

- Images must be PNG format with a minimum width of 679 px.

## Text

- Avoid using `you`, `we`, `our` and other personal pronouns, except in the quickstart which is intended  as a companion journey. Alternatives are (e.g. instead of `this is where you will deploy the application`):
  - Rewrite the sentence using passive voice e.g. `this is where the application will be deployed`.
  - Rewrite the sentence to personify the subject e.g. `the application will be deployed here`.
  - Rewrite the sentence using active voice and a verb e.g. `deploy the application to...`.

## Cookbook

### Recipe categories

The Cookbook is organized into categories, each of which has two or more recipes. New categories may be added at will; however, ideally a category should contain at least two recipes.

### Recipes

Each recipe requires only:

- a level-3 heading, which must begin with a verb (required);
- a one-sentence description of what the recipe does and additional notes for any placeholder replacements to be performed when using the recipe (required);
- the code itself, which must be presented for each SDK (required);
- one or more usage examples of `dagger call`, in an "Example" level-4 heading (required);
- a "learn more" link (optional).

### Code listings

- Code listings can come from two sources:
  - Snippets used in an existing guide
  - Snippets created specifically for the cookbook (usually stored in `./manuals/developer/cookbook/snippets/RECIPE/LANGUAGE/FILE`)
- Code listings must be presented for each language SDK unless not relevant/not technically feasible for that language (e.g. a recipe for "using a magefile" would only be relevant for Go).
- Code listings must be presented in a tabbed interface with the order of tabs set to `Go`, `Python` and `TypeScript`.
