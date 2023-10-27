package io.dagger.sample;

import io.dagger.client.Client;
import io.dagger.client.Dagger;
import java.util.List;

@Description("List the files and directories from the host working dir in a container")
public class ListHostDirectoryContents {
  public static void main(String... args) throws Exception {
    try (Client client = Dagger.connect()) {
      List<String> entries = client.host().directory(".").entries();
      entries.stream().forEach(System.out::println);
    }
  }
}
