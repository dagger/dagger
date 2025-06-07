package io.dagger.sample;

import io.dagger.client.AutoCloseableClient;
import io.dagger.client.Container;
import io.dagger.client.Dagger;
import io.dagger.client.exception.DaggerQueryException;
import java.util.List;

@Description("Run a binary in a container")
public class RunContainerWithError {
  public static void main(String... args) throws Exception {
    try (AutoCloseableClient client = Dagger.connect()) {
      Container container = client.container().from("maven:3.9.2").withExec(List.of("cat", "/"));

      String version = container.stdout();
      System.out.println("Hello from Dagger and " + version);
    } catch (DaggerQueryException dqe) {
      System.out.println("Test pipeline failure: " + dqe.toFullMessage());
    }
  }
}
