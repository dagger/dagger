package io.dagger.sample;

import io.dagger.client.Client;
import io.dagger.client.Container;
import io.dagger.client.Dagger;
import io.dagger.client.Directory;
import java.util.List;

@Description("Clone the Dagger git repository and build from a Dockerfile")
public class BuildFromDockerfile {
  public static void main(String... args) throws Exception {
    try (Client client = Dagger.connect()) {
      Directory dir = client.git("https://github.com/dagger/dagger").tag("v0.6.2").tree();

      Container daggerImg = client.container().build(dir);

      String stdout = daggerImg.withExec(List.of("version")).stdout();
      System.out.println(stdout);
    }
  }
}
