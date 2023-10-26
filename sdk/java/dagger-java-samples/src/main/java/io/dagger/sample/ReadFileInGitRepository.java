package io.dagger.sample;

import io.dagger.client.Client;
import io.dagger.client.Dagger;
import java.io.BufferedReader;
import java.io.StringReader;

@Description("Clone the Dagger git repository and print the first line of README.md")
public class ReadFileInGitRepository {
  public static void main(String... args) throws Exception {
    try (Client client = Dagger.connect()) {
      String readme =
          client
              .git("https://github.com/dagger/dagger")
              .tag("v0.9.0")
              .tree()
              .file("README.md")
              .contents();

      System.out.println(new BufferedReader(new StringReader(readme)).readLine());

      // Output: ## What is Dagger?
    }
  }
}
