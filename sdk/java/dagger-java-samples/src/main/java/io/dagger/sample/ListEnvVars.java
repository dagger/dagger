package io.dagger.sample;

import io.dagger.client.Client;
import io.dagger.client.Dagger;
import io.dagger.client.EnvVariable;
import java.util.List;

@Description("List container environment variables")
public class ListEnvVars {
  public static void main(String... args) throws Exception {
    try (Client client = Dagger.connect()) {
      List<EnvVariable> env =
          client.container().from("alpine").withEnvVariable("MY_VAR", "some_value").envVariables();
      for (EnvVariable var : env) {
        System.out.printf("%s = %s\n", var.name(), var.value());
      }
    }
  }
}
