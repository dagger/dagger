package io.dagger.sample;

import io.dagger.client.Client;
import io.dagger.client.Dagger;
import java.util.List;

@Description("Mount a host directory in container")
public class MountHostDirectoryInContainer {
  public static void main(String... args) throws Exception {
    try (Client client = Dagger.connect()) {
      String contents =
          client
              .container()
              .from("alpine")
              .withDirectory("/host", client.host().directory("."))
              .withExec(List.of("ls", "/host"))
              .stdout();

      System.out.println(contents);
    }
  }
}
