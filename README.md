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

Look at the `dagger-java-samples` module for more code samples.