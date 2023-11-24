---
slug: /zenith/developer/go/525021/quickstart
displayed_sidebar: "zenith"
toc_max_heading_level: 2
title: "Quickstart"
---

# Quickstart

{@include: ../../partials/_experimental.md}

## Introduction

{@include: ../../partials/_developer_quickstart_introduction.md}

## Requirements

This quickstart assumes that:

- You have a good understanding of the Dagger Go SDK. If not, refer to the [Go](https://pkg.go.dev/dagger.io/dagger) SDK reference.
- You have the Dagger CLI installed. If not, [install Dagger](../../../current/cli/465058-install.md).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Step 1: Initialize a new module

1. Create a new directory on your filesystem and run `dagger mod init` to bootstrap your first module. We'll call it `potato` here, but you can choose your favorite food.

  ```sh
  mkdir potato/
  cd potato/

  # initialize Dagger module
  dagger mod init --name=potato --sdk=go
  ```

  This will generate a `dagger.json` module file, an initial `main.go` source file, as well as a generated `dagger.gen.go` and `internal` folder for the generated module code.

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

Let's try changing the `main.go` file.

1. The module is named `potato`, so that means all methods on the `Potato` type are published as functions. Let's replace the auto-generated template with something simpler:

  ```go file=./snippets/quickstart/step2/main.go
  ```

  Module functions are flexible in what parameters they can take. You can include
  an optional `context.Context`, and an optional `error` result. These are all
  valid variations of the above:

  ```go
  func (m *Potato) HelloWorld() string
  func (m *Potato) HelloWorld() (string, error)
  func (m *Potato) HelloWorld(ctx context.Context) string
  func (m *Potato) HelloWorld(ctx context.Context) (string, error)
  ```

1. Run `dagger mod sync` to regenerate the module code:

  ```sh
  dagger mod sync
  ```

  :::important
  You must run `dagger mod sync` after every change to your module's interface (when you add/remove functions or change their parameters and return types).
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

  ```go file=./snippets/quickstart/step3a/main.go
  ```

  The optional parameters are specified using the special `Optional` helper type, which represents optional values. Any method arguments that use this type will be set as optional in the generated API. These optional parameters can be set using `dagger call` or `dagger query` (exactly as if they'd been specified as top-level options).

  In this example, the optional parameters assigned default values, and `GetOr()` returns either the internal value of the optional or the given default value if the value was not explicitly set by the caller.

  Here's an example of calling the function with optional parameters:

  ```sh
  dagger call hello-world --count 10 --mashed true
  ```

  or

  ```sh
  echo '{potato{helloWorld(count:10, mashed:true)}}' | dagger query
  ```

1. Update the function to return a custom `PotatoMessage` type:

  ```go file=./snippets/quickstart/step3b/main.go
  ```

  Test it using `dagger call` or `dagger query`:

  ```sh
  dagger call hello-world --message "I am a potato" message
  dagger call hello-world --message "I am a potato" from
  ```

  or

  ```sh
  echo '{potato{helloWorld(message: "I am a potato"){message, from}}}' | dagger query
  ```

:::tip
Use `dagger call --help` to get help on the commands and flags available.
:::

## Example: Write a vulnerability scanning module

The example module in the previous sections was just that - an example. Next, let's put everything you've learnt to the test, by building a module for a real-world use case: scanning a container image for vulnerabilities with [Trivy](https://trivy.dev/).

1. Initialize a new module:

  ```shell
  mkdir trivy/
  cd trivy/
  dagger mod init --name=trivy --sdk=go
  ```

1. Replace the generated `main.go` file with the following code:

  ```go file=./snippets/quickstart/trivy/main.go
  ```

  In this example, the `ScanImage()` function accepts four parameters (apart from the context):
    - A reference to the container image to be scanned (required);
    - A severity filter (optional);
    - The exit code to use if scanning finds vulnerabilities (optional);
    - The reporting format (optional).

  `dag` is the Dagger client, which is pre-initialized. It contains all the core types (like `Container`, `Directory`, etc.), as well as bindings to any dependencies your module has declared.

  The function code performs the following operations:
    - It uses the `dag` client's `Container().From()` method to initialize a new container from a base image. In this example, the base image is the official Trivy image `aquasec/trivy:latest`. This method returns a `Container` representing an OCI-compatible container image.
    - It uses the `Container.WithExec()` method to define the command to be executed in the container - in this case, the `trivy image` command for image scanning. It also passes the optional parameters to the command. The `WithExec()` method returns a revised `Container` with the results of command execution.
    - It retrieves the output stream of the command with the `Container.Stdout()` method and prints the result to the console.

1. Test the function using `dagger call`:

{@include: ../../partials/_developer_quickstart_trivy_test.md}

## Conclusion

{@include: ../../partials/_developer_quickstart_conclusion.md}

- Guide on [programming a Dagger module to build, test and publish an application image](./457202-test-build-publish.md)

## Appendix A: Troubleshooting

If you come across bugs, here are some simple troubleshooting suggestions.

{@include: ../../partials/_developer_troubleshooting.md}
