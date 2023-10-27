package io.dagger.sample;

import io.dagger.client.Client;
import io.dagger.client.Container;
import io.dagger.client.Dagger;
import java.util.List;

@Description("Run a binary in a container")
public class RunContainer {
  public static void main(String... args) throws Exception {
    try (Client client = Dagger.connect()) {
      Container container =
          client.container().from("maven:3.9.2").withExec(List.of("mvn", "--version"));

      String version = container.stdout();
      System.out.println("Hello from Dagger and " + version);
    }
  }
}
