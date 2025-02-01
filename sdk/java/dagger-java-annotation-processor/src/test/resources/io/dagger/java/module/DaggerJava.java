package io.dagger.java.module;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.module.Base;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import io.dagger.module.annotation.Optional;
import java.util.List;
import java.util.concurrent.ExecutionException;
import org.apache.commons.lang3.StringUtils;

/** Dagger Java Module main object */
@Object
public class DaggerJava extends Base {
  public DaggerJava() {
    super();
  }

  /**
   * Returns a container that echoes whatever string argument is provided
   *
   * @param stringArg string to echo
   * @return container running echo
   */
  @Function
  public Container containerEcho(@Optional(defaultValue = "Hello Dagger") String stringArg) {
    if (StringUtils.isEmpty(stringArg)) {
      stringArg = "Hello World!";
    }
    return dag.container().from("alpine:latest").withExec(List.of("echo", stringArg));
  }

  /**
   * Returns lines that match a pattern in the files of the provided Directory
   *
   * @param directoryArg Directory to grep
   * @param pattern Pattern to search for in the directory
   * @return Standard output of the grep command
   */
  @Function
  public String grepDir(Directory directoryArg, String pattern)
      throws InterruptedException, ExecutionException, DaggerQueryException {
    return dag.container()
        .from("alpine:latest")
        .withMountedDirectory("/mnt", directoryArg)
        .withWorkdir("/mnt")
        .withExec(List.of("grep", "-R", pattern, "."))
        .stdout();
  }

  @Function
  public DaggerJava itself() {
    return this;
  }

  @Function
  public boolean isZero(int value) {
    return value == 0;
  }

  @Function
  public int[] doThings(String[] stringArray, List<Integer> ints, List<Container> containers) {
    int[] intsArray = {stringArray.length, ints.size()};
    return intsArray;
  }
}
