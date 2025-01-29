> [!WARNING]
> This SDK is experimental. Please do not use it for anything
> mission-critical. Possible issues include:
>
> - Missing features
> - Stability issues
> - Performance issues
> - Lack of polish
> - Upcoming breaking changes
> - Incomplete or out-of-date documentation

> [!IMPORTANT]
> The Dagger Java SDK requires Dagger v0.9.0 or later

# dagger-java-sdk

![main workflow](https://github.com/dagger/dagger/actions/workflows/test.yml/badge.svg?branch=main)

A [Dagger.io](https://dagger.io) SDK written in Java.

## Modules

> [!WARNING]
> Support of Dagger modules is in progress and might be incomplete.

### Create a new module

```console
$ dagger init --sdk=java my-java-module

$ tree my-java-module
my-java-module
├── dagger.json
├── pom.xml
└── src
    └── main
        └── java
            └── io
                └── dagger
                    └── sample
                        └── module
                            ├── MyJavaModule.java
                            └── package-info.java

8 directories, 4 files
```

### List functions and call them

```console
$ dagger functions -m my-java-module

Name             Description
container-echo   Returns a container that echoes whatever string argument is provided
grep-dir         Returns lines that match a pattern in the files of the provided Directory
```

```console
$ dagger call -q -m my-java-module container-echo "hello dagger" stdout

hello dagger

```

### Develop modules

In addition to the module source files, the SDK java files and all the generated source files like the entrypoint are available under `target/` directory.

If they are missing or to refresh them, run:

```bash
dagger develop
```

- `target/generated-sources/dagger-io` contains the generic Java SDK for dagger
- `target/generated-sources/dagger-module` contains the code generated for this specific module
- `target/generated-sources/entrypoint` contains the entrypoint used to run the module

### How it's done

The Java SDK is composed of three main parts:

- `dagger-codegen-maven-plugin`

    This plugin will be used to generate the SDK code, from the introspection file.
    This means including the ability to call other modules (not part of the main dagger SDK)

- `dagger-java-annotation-processor`

    This will read dagger specific annotations (`@Module`, `@Object`, `@Function`, `@Optional`)
    and generate the entrypoint to register the module and invoke the functions

- `dagger-java-sdk`

    The actual SDK code, where the generated code will be written. It will include all the required types to discuss with the dagger engine.

## Build

### Requirements

- Java 17+

### Build

Simply run maven to build the jars, run all tests (unit and integration) and install them in your
local `${HOME}/.m2` repository

```bash
./mvnw clean install 
```

### Troubleshoot generated code

To inspect the code that gets generated, run:

```bash
./mvnw package
```

The generated code will exist under
`sdk/java/dagger-java-sdk/target/generated-sources/dagger/io/dagger/client`.

### Javadoc

To generate the Javadoc (site and jar), use the `javadoc` profile.
The javadoc are built in `./dagger-java-sdk/target/apidocs/index.html`

```bash
./mvnw package -Pjavadoc
```

## Usage

in your project's `pom.xml` add the dependency

```xml

<dependency>
  <groupId>io.dagger</groupId>
  <artifactId>dagger-java-sdk</artifactId>
  <version>0.6.2-SNAPSHOT</version>
</dependency>
```

Here is a code snippet using the Dagger client

```java
package io.dagger.sample;

import io.dagger.client.Client;
import io.dagger.client.Dagger;

import java.util.List;

public class GetDaggerWebsite {

  public static void main(String... args) throws Exception {
    try (Client client = Dagger.connect()) {
      String output = client
          .container()
          .from("alpine")
          .withExec(List.of("apk", "add", "curl"))
          .withExec(List.of("curl", "https://dagger.io"))
          .stdout();

      System.out.println(output.substring(0, 300));
    }
  }
}
```

### Run sample code snippets

The `dagger-java-samples` module contains code samples.

Run the samples with this command:

```bash
# Build the packages and run the samples 
./mvnw package -Prun-samples
```

Then select the sample to run:

```
=== Dagger.io Java SDK samples ===
   1  io.dagger.sample.RunContainer                   Run a binary in a container
   2  io.dagger.sample.GetDaggerWebsite               Fetch the Dagger website content and print the first 300 characters
   3  io.dagger.sample.ListEnvVars                    List container environment variables
   4  io.dagger.sample.MountHostDirectoryInContainer  Mount a host directory in container
   5  io.dagger.sample.ListHostDirectoryContents      List the files and directories from the host working dir in a container
   6  io.dagger.sample.ReadFileInGitRepository        Clone the Dagger git repository and print the first line of README.md
   7  io.dagger.sample.PublishImage                   Publish a container image to a remote registry
   8  io.dagger.sample.BuildFromDockerfile            Clone the Dagger git repository and build from a Dockerfile
   9  io.dagger.sample.CreateAndUseSecret             Create a secret with a Github token and call a Github API using this secret
  10  io.dagger.sample.TestWithDatabase               Run a sample CI test pipeline with MariaDB, Drupal and PHPUnit
  11  io.dagger.sample.HostToContainerNetworking      Expose a service from a container to the host
  12  io.dagger.sample.ContainerToHostNetworking      Expose MySQL service running on the host to client containers
   q  exit

Select sample:
```

### Run pipeline with Dagger CLI

To run a Java pipeline, the Java SDK is needed as a JAR file containing all dependencies.

This self-contained JAR file can be built with this command:

```bash
./mvnw clean package -Pbigjar,release
```

To run a sample, the classpath has to contain the Java SDK JAR file and the samples JAR file.

The following command uses the Dagger CLI to start the ListEnvVars sample:

```bash
dagger run java -cp dagger-java-sdk/target/dagger-java-sdk-1.0.0-SNAPSHOT-jar-with-dependencies.jar:dagger-java-samples/target/dagger-java-samples-1.0.0-SNAPSHOT.jar io.dagger.sample.ListEnvVars
```

**Warning**: It may happen that the pipeline does not terminate after the execution of the sample code.

## Customizing the code generation

It is possible to change the dagger version targeted by the SDK by setting the maven
property `daggerengine.version`.

```shell
# Build the SDK for Dagger 0.8.1
./mvnw package -Ddaggerengine.version=0.8.1
```

> **Warning**
> If the targeted version mismatches the actual CLI version, the code generation will fail

By setting the variable to the special `local` value (or the alias `devel`), it is possible to query
a dagger CLI to generate the API schema.

It is also possible to specify the Dagger CLI binary to use to generate the schema...

Either by setting the `_EXPERIMENTAL_DAGGER_CLI_BIN` environment variable

```shell
# Build the SDK for a specific Dagger CLI
_EXPERIMENTAL_DAGGER_CLI_BIN=/path/to/dagger ./mvnw package -Ddaggerengine.version=local
```

or by setting the maven property `dagger.bin`

```shell
# Build the SDK for a specific Dagger CLI
./mvnw package -Ddaggerengine.version=local -Ddagger.bin=/path/to/dagger
```

## Upgrade to a new Dagger Engine version

In order to upgrade the SDK to a new engine version follow these steps:

1. Download the new dagger CLI (or install it via the package manager of your choice)
2. Bump dagger engine dependency by updating the `daggerengine.version` property
   in `sdk/java/pom.xml` file
3. Generate the API schema for the new engine and copy it
   the `dagger-codegen-maven-plugin/src/main/resources/schemas` directory

```shell
# in sdk/java directory
./mvnw install -pl dagger-codegen-maven-plugin
./mvnw -N dagger-codegen:generateSchema -Ddagger.bin=/path/to/dagger/bin
NEW_VERSION=$(./mvnw help:evaluate -q -DforceStdout -Dexpression=daggerengine.version)
cp ./target/generated-schema/schema.json dagger-codegen-maven-plugin/src/main/resources/schemas/schema-$NEW_VERSION.json
```

## Test without building

For those who would like to test without having to build the SDK:

1. Go to the workflows on the main
   branch: https://github.com/jcsirot/dagger-java-sdk/actions?query=branch%3Amain
2. Click on the most recent executed workflow
3. Scroll down to the bottom of the page and download the `jar-with-dependencies` artifact

> **Warning**
> It is a zip file. Unzip it to retrieve the jar file.

4. Compile and run your sample pipeline with this jar file in the classpath

```bash
# Compile
javac -cp dagger-java-sdk-[version]-jar-with-dependencies.jar GetDaggerWebsite.java
# Run
java -cp dagger-java-sdk-[version]-jar-with-dependencies.jar:. GetDaggerWebsite
```

5. Enjoy 😁

## Contributing

The Java source code is automatically formatted on each build using [google-java-format](https://github.com/google/google-java-format) through [fmt-maven-plugin](https://github.com/spotify/fmt-maven-plugin).
