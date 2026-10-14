package io.dagger.sample;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.AutoCloseableClient;
import io.dagger.client.Container;
import io.dagger.client.Dagger;
import io.dagger.client.Json;
import io.dagger.client.exception.DaggerExecException;
import java.util.List;
import org.apache.commons.lang3.StringUtils;

@Description("Run a binary in a container")
public class RunContainerWithError {
  public static void main(String... args) throws Exception {
    try (AutoCloseableClient client = Dagger.connect()) {
      Container container = client.container().from("maven:3.9.2").withExec(List.of("cat", "/"));

      String version = container.stdout();
      System.out.println("Hello from Dagger and " + version);
    } catch (DaggerExecException dqe) {
      System.out.println("Test pipeline failure simple message: " + dqe.toSimpleMessage());
      System.out.println("Test pipeline failure enhanced message: " + dqe.toEnhancedMessage());
      System.out.println("Test pipeline failure full message: " + dqe.toFullMessage());
      System.out.println(
          "Test pipeline failure full message: "
              + dag()
                  .error(dqe.getMessage())
                  .withValue("stdout", Json.from(dqe.getStdOut()))
                  .withValue("stderr", Json.from(dqe.getStdErr()))
                  .withValue("cmd", Json.from(StringUtils.join(dqe.getCmd())))
                  .withValue("exitCode", Json.from(StringUtils.join(dqe.getExitCode())))
                  .withValue("path", Json.from(StringUtils.join(dqe.getPath())))
                  .toString());
    }
  }
}
