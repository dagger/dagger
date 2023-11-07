---
slug: /zenith/developer/go/457202/test-build-publish
displayed_sidebar: "zenith"
toc_max_heading_level: 2
authors: ["Erik Sipsma"]
date: "2023-11-07"
---

# Test, Build and Publish an Application using a Module

## Introduction

This guide walks you through the process of creating a Dagger module from scratch to test, build and publish a Node.js application image. You will learn how to:

- Initialize a new Dagger module and add dependencies to it
- Write custom functions using the Go SDK to test, build and package an application
- Use containers as function input arguments and return values
- Debug module code using an interactive shell
- Test module code by exposing services or containers returned by a function on the host

## Requirements

This guide assumes that:

- You have a good understanding of the JavaScript programming language.
- You know the basics of programming Dagger modules. If not, refer to the [quickstart](./525021-quickstart.md).
- You have a Go development environment. If not, install [Go](https://go.dev/doc/install).
- You have the Dagger CLI installed in your development environment. If not, [install the Dagger CLI](../../../current/cli/979595-reference.md).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have a Node.js Web application. If not, follow the steps in Appendix A to [create an example Node.js application](#appendix-a-create-an-example-application).

## Step 1: Initialize a new module

The example module used in this guide builds, tests and publishes a Node.js application.

Begin by running `dagger mod init` in your application directory to bootstrap a new module:

```sh
dagger mod init --name=mymod --sdk=go
```

This will generate a `dagger.json` module file, an initial `main.go` source file, as well as a generated `dagger.gen.go` and `internal` folder for the generated module code.

## Step 2: Add a function to build the application base image

The first step is to add a function to build a base image containing the application source code and runtime. This base image will serve as an input to other functions.

Since the application is a Node.js application, it's convenient to use the `node` module from the [Daggerverse](https://daggerverse.dev), which provides a set of ready-made functions to manage a Node.js project.

1. Add the `node` module as a dependency:

  ```shell
  dagger mod install github.com/quartz-technology/daggerverse/node@9ce087b83aa8b85f828d7d92ce39bd7c055cfc0f
  ```

1. Update the generated `main.go` file with the following code:

  ```go
  package main

  import (
    "context"
  )

  type Mymod struct {}

  const defaultNodeVersion = "16"

  func (m *Mymod) buildBase(nodeVersion Optional[string]) *Node {
    return dag.Node().
      WithVersion(nodeVersion.GetOr(defaultNodeVersion)).
      WithNpm().
      WithSource(dag.Host().Directory(".", HostDirectoryOpts{
        Exclude: []string{".git", "**/node_modules"},
      })).
      Install(nil)
  }
  ```

  This function does the following:

    - It calls the `node` module's `WithVersion()` method via the `dag` client. This method returns a `node` container image with the given Node.js version. This container image is represented as a `Container` object.
    - It calls the module's `Container.WithNpm()` method, which returns a revised `Container` after adding the `npm` package manager and a cache volume for `npm`.
    - It calls the module's `Container.WithSource()` method, which returns a revised `Container` including the application source code and a cache volume for Node.js modules.
    - It calls the module's `Container.Install()` method, which runs `npm install` in the container and returns a revised `Container` including the application's dependencies.

  :::note
  `dag` is the Dagger client, which is pre-initialized. It contains all the core types (like `Container`, `Directory`, etc.), as well as bindings to any dependencies your module has declared (like `node`).
  :::

## Step 3: Add a function to test the application

The return value of the `buildBase()` function is a `Container` with the application source code, Node.js runtime and cahe volumes. This is everything needed to test, build and publish the application.

Add a new `Test()` function that runs tests for the example application, by executing the `npm test` command:

```go
func (m *Mymod) Test(ctx context.Context, nodeVersion Optional[string]) (string, error) {
	return m.buildBase(nodeVersion).
		Run([]string{"test", "--", "--watchAll=false"}).
		Stderr(ctx)
}
```

This function does the following:

- It calls the `buildBase()` function to obtain a `Container` with the application source code, Node.js runtime and cache volumes.
- It calls the module's `Container.Run()` method, which returns a revised `Container` after setting the commands to run in the container image - in this case, the command `npm test -- --watchAll-false`.
- It uses the `Container.Stderr()` method to return the error stream of the last executed command. No error output implies successful execution (all tests pass).

Test the function by running it as below:

```shell
dagger call test
```

Here's an example of the output you will see:

```shell
✔ dagger call test [58.42s]
┃ PASS src/App.test.js
┃   ✓ renders learn react link (101 ms)
┃
┃ Test Suites: 1 passed, 1 total
┃ Tests:       1 passed, 1 total
┃ Snapshots:   0 total
┃ Time:        7.619 s
┃ Ran all test suites.
```

:::tip
This listing uses the `node` module's `Container.Run().Stderr()` method to explicitly specify the test command and print its error stream. As an alternative, the `node` module also exposes a `Test()` method, which executes the `npm run test` command and prints its output stream.
:::

## Step 4: Add a function to build the application

If your application passes all its tests, the typical next step is to build it.

Add a new `Build()` function that creates a production build of the example application:

```go
func (m *Mymod) Build(nodeVersion Optional[string]) *Directory {
	return m.buildBase(nodeVersion).Build().Container().Directory("./build")
}
```

This function does the following:

- It calls the `buildBase()` function to obtain a `Container` with the application source code, Node.js runtime and cache volumes.
- It calls the module's `Container.Build()` method, which returns a revised `Container` after setting the `npm run build` command to run in the container image.
- It obtains a reference to the `build/` directory in the container with the `Container.Directory()` method. This method returns a `Directory` object.

:::note
The `npm run build` command is appropriate for a React application, but other applications are likely to use different commands. Modify your Dagger pipeline accordingly.
:::

Test the function by running it as below:

```shell
dagger call build
```

Here's an example of the output you will see:

```shell
✔ dagger call build [2m12.6s]
┃ asset-manifest.json
┃ favicon.ico
┃ index.html
┃ logo192.png
┃ logo512.png
┃ manifest.json
┃ robots.txt
┃ static
```

## Step 5: Add a function to publish the application image

At this point, your Dagger module has functions to test and build the application. However, Dagger SDKs also have built-in support to publish container images to remote registries.

One such registry is [ttl.sh](https://ttl.sh), an ephemeral Docker registry. The [Daggerverse](https://daggerverse.dev) already includes a `ttlsh` module to publish to this registry.

1. Add the `ttlsh` module as a dependency in your module:

  ```shell
  dagger mod install github.com/shykes/daggerverse/ttlsh@16e40ec244966e55e36a13cb6e1ff8023e1e1473
  ```

1. Update the module and add new `Package()` and `Publish()` functions to copy the built application into an NGINX web server container and deliver the result to the registry:

  ```go
  func (m *Mymod) Package(nodeVersion Optional[string]) *Container {
    return dag.Container().From("nginx:1.23-alpine").
      WithDirectory("/usr/share/nginx/html", m.Build(nodeVersion))
  }

  func (m *Mymod) Publish(ctx context.Context, nodeVersion Optional[string]) (string, error) {
    return dag.Ttlsh().Publish(ctx, m.Package(nodeVersion))
  }
  ```

    - The `Package()` function calls the `dag` client's `Container().From()` method to initialize a new container from a base image - here, the `nginx:1.23-alpine` image. The `From()` method returns a new `Container` object with the result. It uses the `Container.WithDirectory()` method to write the `Directory` returned by the `Build()` function to the `/usr/share/nginx/html` path in the container and return a revised `Container`.
    - The `Publish()` function accepts a `Container` as input. It calls the `ttlsh` module's `Publish()` method to publish the container image to the [ttl.sh](https://ttl.sh) registry and return the image identifier.

Test the code by running the command below:

```shell
dagger call publish
```

Here's an example of the output you will see:

```shell
✔ dagger call publish [1m12.8s]
┃ ttl.sh/pensive_murdock:10m@sha256:2c483d2e6aec5f950221363aaf1cdde5aceab906dd85d2b63e97acda48881b5a
```

## Step 6: Test and debug the module

Apart from the usual Go debugging techniques, Dagger provides two commands that come in very handy when debugging modules:

### dagger shell

The `dagger shell` command can be used to open an interactive shell session with any `Container` type returned by a function. This is very useful for debugging and experimenting since it allows you to interact with containers directly.

By default, `dagger shell` will execute the container's entrypoint, but you can override this with the `--entrypoint` flag.

Try it with the command below:

```shell
dagger shell package --entrypoint /bin/sh
```

This command drops you into an interactive shell, allowing you to directly execute commands in the container returned by the `package` function, as in the example below:

```shell
/ # cd /usr/share/nginx/html
/usr/share/nginx/html # ls
50x.html             favicon.ico          logo192.png          manifest.json        static
asset-manifest.json  index.html           logo512.png          robots.txt
/usr/share/nginx/html #
```

### dagger up

The `dagger up` command allows `Service` and `Container` types returned by a function to be executed and have any exposed ports forwarded to the host machine. This has many potential use cases, such as manually testing web servers or databases directly from the host browser or host system.

In order for this to work, the service/container returned by the function must have the `Container.withExposedPort` field defining one or more exposed ports. So, add a new `PackageService()` function as shown below:

```go
func (m *Mymod) PackageService(nodeVersion Optional[string]) *Service {
	return m.Package().
		WithExposedPort(8080).
		AsService()
}
```

Then, use the `dagger up` command to build the application and serve it with NGINX:

```shell
dagger up --native package-service
```

You should now be able to access the application by browsing to `http://localhost:8080` on the host (replace `localhost` with your Docker host's network name if accessing it remotely).

## Conclusion

This guide walked you through the process of creating a Dagger module from scratch to test, build and publish a Node.js application image. It explained how to create a module, add functions to it, and work with containers and services as function inputs and outputs. It also demonstrated how to use modules from the Daggerverse to speed up your module development, by reusing functionality developed by the Dagger community.

Continue your journey into Dagger programming with the following resources:

- The [Daggerverse](https://daggerverse.dev), an online catalog of Dagger modules for you to use and learn from
- Guide on [advanced module programming](./191108-advanced-programming.md)
- Guide on [publishing modules to the Daggerverse](../821742-publishing-modules.md)
- Reference documentation for the [Go](https://pkg.go.dev/dagger.io/dagger) SDK

## Appendix A: Create an example application

This tutorial assumes that you have a Node.js Web application. If not, follow the steps below to create an example React application.

1. Create a directory for the application:

  ```shell
  mkdir myapp
  cd myapp
  ```

1. Create a skeleton application:

  ```shell
  npx create-react-app .
  ```

1. Make a minor modification to the application's index page:

  ```shell
  sed -i -e 's/Express/Dagger/g' routes/index.js
  ```
