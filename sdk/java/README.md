> **Warning** This SDK is experimental. Please do not use it for anything
> mission-critical. Possible issues include:

- Missing features
- Stability issues
- Performance issues
- Lack of polish
- Upcoming breaking changes
- Incomplete or out-of-date documentation

# dagger-java-sdk

![main workflow](https://github.com/dagger/dagger/actions/workflows/test.yml/badge.svg?branch=main)

A [Dagger.io](https://dagger.io) SDK written in Java.

## Build

### Requirements

- Java 17+

### Build

Simply run maven to build the jars, run all tests (unit and integration) and install them in your
local `${HOME}/.m2` repository

```bash
./mvnw clean install 
```

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
      String output = client.pipeline("test")
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
# Build the packages 
./mvnw package
# Run the samples 
./mvnw exec:java -pl dagger-java-samples
```

Then select the sample to run:

```
=== Dagger.io Java SDK samples ===
  1 - io.dagger.sample.RunContainer
  2 - io.dagger.sample.GetDaggerWebsite
  3 - io.dagger.sample.ListEnvVars
  4 - io.dagger.sample.MountHostDirectoryInContainer
  5 - io.dagger.sample.ListHostDirectoryContents
  6 - io.dagger.sample.ReadFileInGitRepository
  7 - io.dagger.sample.GetGitVersion
  8 - io.dagger.sample.CreateAndUseSecret
  9 - io.dagger.sample.GetGitVersion
  q - exit

Select sample:
```

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
2. Bump dagger engine dependency by updating the `daggerengie.version` property
   in `sdk/java/pom.xml` file
3. Generate the API schema for the new engine and copy it
   the `dagger-codegen-maven-plugin/src/main/resources/schemas` directory

```shell
# in sdk/java directory
mvn install -pl dagger-codegen-maven-plugin
mvn -N dagger-codegen:generateSchema -Ddagger.bin=/path/to/dagger/bin
NEW_VERSION=$(mvn help:evaluate -q -DforceStdout -Dexpression=daggerengine.version)
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
