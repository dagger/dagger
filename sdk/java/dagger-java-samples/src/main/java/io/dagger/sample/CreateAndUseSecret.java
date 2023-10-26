package io.dagger.sample;

import io.dagger.client.*;
import java.util.List;

@Description("Create a secret with a Github token and call a Github API using this secret")
public class CreateAndUseSecret {
  public static void main(String... args) throws Exception {
    String token = System.getenv("GH_API_TOKEN");
    if (token == null) {
      token = new String(System.console().readPassword("GithHub API token: "));
    }
    try (Client client = Dagger.connect()) {
      Secret secret = client.setSecret("ghApiToken", token);

      // use secret in container environment
      String out =
          client
              .container()
              .from("alpine:3.17")
              .withSecretVariable("GITHUB_API_TOKEN", secret)
              .withExec(List.of("apk", "add", "curl"))
              .withExec(
                  List.of(
                      "sh",
                      "-c",
                      "curl \"https://api.github.com/repos/dagger/dagger/issues\" --header \"Accept: application/vnd.github+json\" --header \"Authorization: Bearer $GITHUB_API_TOKEN\""))
              .stdout();

      // print result
      System.out.println(out);
    }
  }
}
