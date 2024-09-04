package io.dagger.sample;

import io.dagger.client.Client;
import io.dagger.client.Dagger;
import java.util.List;

@Description("Fetch the Dagger website content and print the first 300 characters")
public class GetDaggerWebsite {
  public static void main(String... args) throws Exception {
    try (Client client = Dagger.connect()) {
      String output =
          client
              .container()
              .from("alpine")
              .withExec(List.of("apk", "add", "curl"))
              .withExec(List.of("curl", "https://dagger.io"))
              .stdout();

      System.out.println(output.substring(0, 300));
    }
  }
}
