package io.dagger.sample;

import io.dagger.client.AutoCloseableClient;
import io.dagger.client.Container;
import io.dagger.client.Dagger;
import io.dagger.client.Directory;
import java.util.List;

@Description("Clone the Dagger git repository and build from a Dockerfile")
public class BuildFromDockerfile {
  public static void main(String... args) throws Exception {
    try (AutoCloseableClient client = Dagger.connect()) {
      Directory dir = client.git("https://github.com/dagger/dagger").tag("v0.6.2").tree();
      Container daggerImg = dir.dockerBuild();

      String stdout = daggerImg.withExec(List.of("version")).stdout();
      System.out.println(stdout);
    }
  }
}
