# dagger-java-sdk

A [Dagger.io](https://dagger.io) SDK written in Java.

> **Warning**
> This project is still a Work in Progress

## Build

### Requirements

- Java 17+

### Build

Simply run maven to build the jars, run all tests (unit and integration) and install them in your local `${HOME}/.m2` repository

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
    <groupId>org.chelonix.dagger</groupId>
    <artifactId>dagger-java-sdk</artifactId>
    <version>0.6.2-SNAPSHOT</version>
  </dependency>
```

Here is a code snippet using the Dagger client

```java
package org.chelonix.dagger.sample;

import org.chelonix.dagger.client.Client;
import org.chelonix.dagger.client.Dagger;

import java.util.List;

public class GetDaggerWebsite {
    public static void main(String... args) throws Exception {
        try(Client client = Dagger.connect()) {
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
  1 - org.chelonix.dagger.sample.RunContainer
  2 - org.chelonix.dagger.sample.GetDaggerWebsite
  3 - org.chelonix.dagger.sample.ListEnvVars
  4 - org.chelonix.dagger.sample.MountHostDirectoryInContainer
  5 - org.chelonix.dagger.sample.ListHostDirectoryContents
  6 - org.chelonix.dagger.sample.ReadFileInGitRepository
  7 - org.chelonix.dagger.sample.GetGitVersion
  8 - org.chelonix.dagger.sample.CreateAndUseSecret
  9 - org.chelonix.dagger.sample.GetGitVersion
  q - exit

Select sample:
```
