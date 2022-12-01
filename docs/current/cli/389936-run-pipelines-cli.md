---
slug: /cli/389936/run-pipelines-cli
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";
import BrowserOnly from "@docusaurus/BrowserOnly";

# Run Pipelines from the Command Line

## Introduction

This tutorial teaches you the basics of using Dagger from the command line. You will learn how to:

- Install the Dagger CLI
- Create a shell script to build an application from a Git repository using the Dagger CLI

## Requirements

This tutorial assumes that:

- You have a basic understanding of shell scripting with Bash. If not, [read the Bash reference manual](https://www.gnu.org/software/bash/manual/bash.html).
- You have Bash installed in your development environment. Bash is available for Linux, Windows and macOS.
- You have the `jq` JSON processor installed in your development environment. If not, [download and install `jq`](https://github.com/stedolan/jq).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Step 1: Install the Dagger CLI

{@include: ../partials/_install-cli.md}

## Step 2: Create a Dagger client in Bash

The Dagger CLI offers a `dagger query` sub-command, which provides an easy way to send API queries to the Dagger Engine from the command line.

To see this in action, create a new shell script named `build.sh` and add the following code to it:

```shell file=snippets/get-started/step2/build.sh
```

This script invokes the Dagger CLI's `query` sub-command and passes it a GraphQL API query. This query performs the following operations:

- It requests the `from` field of Dagger's `Container` object type, passing it the address of a container image. To resolve this, Dagger will initialize a container using the specified image and return a `Container` object representing the `alpine:latest` container image.
- Next, it requests the `withExec` field of the `Container` object from the previous step, passing  the `uname -a` command to the field as an array of arguments. To resolve this, Dagger will return a `Container` object containing the execution plan.
- Finally, it requests the `stdout` field of the `Container` object returned in the previous step. To resolve this, Dagger will execute the command and return a `String` containing the results.
- The result of the query is returned as a JSON object. This object is processed with `jq` and the result string is printed to the console.

Add the executable bit to the shell script and then run the script by executing the commands below:

```shell
chmod +x ./build.sh
./build.sh
```

The script outputs a string similar to the one below.

```shell
Linux buildkitsandbox 5.15.0-53-generic #59-Ubuntu SMP Mon Oct 17 18:53:30 UTC 2022 x86_64 Linux
```

## Step 3: Build an application from a remote Git repository

Now that the shell script is functional, the next step is to flesh it out to actually build an application. This tutorial demonstrates the process by cloning the [canonical Git repository for Go](https://go.googlesource.com/example/+/HEAD/hello) and building the "Hello, world" example program from it using the Dagger CLI.

Replace the `build.sh` file from the previous step with the version below (highlighted lines indicate changes):

```shell file=snippets/get-started/step3/build.sh
```

This revision of the script contains two queries stitched together.

The first query:

- requests the `master` branch of the Git source code repository (returned as a `GitRef` object);
- requests the filesystem of that branch (returned as a `Directory` object);
- requests the content-addressed identifier of that `Directory` (returned as a base64-encoded value and interpolated into the second query).

The second query:

- initializes a new `alpine:latest` container (returned as a `Container` object);
- mounts the `Directory` from the first query within the container filesystem at the `/src` mount point (returned as a revised `Container`);
- sets the working directory within the container to the mounted filesystem (returned as a revised `Container`);
- requests execution of the `go build` command (returned as a revised `Container` containing the execution plan);
- retrieves the build artifact (returned as a `File`);
- writes the `File` from the container to the host as a binary file named `dagger-builds-hello`.

The return value of the final `export` field is a Boolean value indicating whether the file was successfully written to the host or not. This value is extracted from the GraphQL API response document using `jq` and evaluated with Bash.

Run the shell script by executing the command below:

```shell
./build.sh
```

As described above, the script retrieves the source code repository, mounts and builds it in a container, and writes the resulting binary file to the host. At the end of the process, the built Go application is available in the working directory on the host, as shown below:

```shell
tree
.
├── build.sh
└── dagger-builds-hello
```

## Conclusion

This tutorial introduced you to the Dagger CLI and its `dagger query` sub-command. It explained how to install the CLI and how to use it to execute Dagger GraphQL API queries. It also provided a working example of how to run a Dagger pipeline from the command line using a Bash shell script.

Use the [API Reference](https://docs.dagger.io/api/reference) and the [CLI Reference](./979595-reference.md) to learn more about the Dagger GraphQL API and the Dagger CLI respectively.
