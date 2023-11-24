---
slug: /zenith/developer/python/419481/quickstart
displayed_sidebar: "zenith"
---

# Quickstart

{@include: ../../partials/_experimental.md}

## Introduction

{@include: ../../partials/_developer_quickstart_introduction.md}

## Requirements

This quickstart assumes that:

- You have a good understanding of the Dagger Python SDK. If not, refer to the [Python](https://dagger-io.readthedocs.org/) SDK reference.
- You have the Dagger CLI installed. If not, [install Dagger](../../../current/cli/465058-install.md).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Step 1: Initialize a new module

1. Create a new directory on your filesystem and run `dagger mod init` to bootstrap your first module. We'll call it `potato` here, but you can choose your favorite food.

  ```sh
  mkdir potato/
  cd potato/

  # initialize Dagger module
  dagger mod init --name=potato --sdk=python
  ```

  This will generate a `dagger.json` module file, an initial `main.py` source file, as well as a generated `dagger.gen.go` and `internal` folder for the generated module code.

1. Test the module. Run the generated `main.go` with the `dagger call` command:

  ```sh
  dagger call container-echo --string-arg 'Hello daggernauts!'
  ```

  :::tip
  When using `dagger call` to call module functions, do not explicitly use the name of the module.
  :::

  An alternative approach is to run the module using a GraphQL query piped through the `dagger query` command:

  ```sh
  echo '{potato{containerEcho(stringArg:"Hello daggernauts!"){stdout}}}' | dagger query
  ```

:::note
When using `dagger call`, all names (functions, arguments, struct fields, etc) are converted into a shell-friendly "kebab-case" style.

When using `dagger query` and GraphQL, all names are converted into a language-agnostic "camelCase" style.
:::

## Step 2: Add a function

Let's try changing the `main.py` file.

TODO

1. Run `dagger mod sync` to regenerate the module code:

  ```sh
  dagger mod sync
  ```

  :::important
  You will need to run `dagger mod sync` after every change to your module's interface (when you add/remove functions or change their parameters and return types).
  :::

1. Test the new function, once again using `dagger call` or `dagger query`:

  ```sh
  dagger call hello-world
  ```

  or

  ```sh
  echo '{potato{helloWorld}}' | dagger query
  ```

## Step 3: Use input parameters and return types

Your module functions can accept and return multiple different types, not just basic built-in types.

1. Update the function to accept multiple parameters (some of which are optional):

TODO

:::tip
Use `dagger call --help` to get help on the commands and flags available.
:::

## Example: Write a vulnerability scanning module

The example module in the previous sections was just that - an example. Next, let's put everything you've learnt to the test, by building a module with a real-world application: scanning a container image for vulnerabilities with [Trivy](https://trivy.dev/).

1. Initialize a new module:

  ```shell
  mkdir trivy/
  cd trivy/
  dagger mod init --name=trivy --sdk=python
  ```

1. Replace the generated `main.py` file with the following code:

TODO

{@include: ../../partials/_developer_quickstart_trivy_test.md}

## Conclusion

{@include: ../../partials/_developer_quickstart_conclusion.md}

## Appendix A: Troubleshooting

If you come across bugs, here are some simple troubleshooting suggestions.

{@include: ../../partials/_developer_troubleshooting.md}
