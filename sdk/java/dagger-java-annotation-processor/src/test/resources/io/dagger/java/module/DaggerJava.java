package io.dagger.java.module;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.client.Platform;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.*;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

/** Dagger Java Module main object */
@Object
public class DaggerJava extends AbstractModule {
  private String notExportedField;

  /** Project source directory */
  public Directory source;

  public String version;

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
  public Container containerEcho(@Default("Hello Dagger") String stringArg) {
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
  public String grepDir(
      @DefaultPath("sdk/java") @Ignore({"**", "!*.java"}) Directory directoryArg,
      @Nullable String pattern)
      throws InterruptedException, ExecutionException, DaggerQueryException {
    if (pattern == null) {
      pattern = "dagger";
    }
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

  /** User must provide the argument */
  @Function
  public String nonNullableNoDefault(String stringArg) {
    if (stringArg == null) {
      throw new RuntimeException("can not be null");
    }
    return stringArg;
  }

  /**
   * If the user doesn't provide an argument, a default value is used. The argument can't be null.
   */
  @Function
  public String nonNullableDefault(@Default("default value") String stringArg) {
    if (stringArg == null) {
      throw new RuntimeException("can not be null");
    }
    return stringArg;
  }

  /**
   * Make it optional but do not define a value. If the user doesn't provide an argument, it will be
   * set to null.
   */
  @Function
  public String nullable(@Default("null") String stringArg) {
    if (stringArg == null) {
      stringArg = "was a null value";
    }
    return stringArg;
  }

  /** Set a default value in case the user doesn't provide a value and allow for null value. */
  @Function
  public String nullableDefault(@Nullable @Default("Foo") String stringArg) {
    if (stringArg == null) {
      stringArg = "was a null value by default";
    }
    return stringArg;
  }

  /** return the default platform as a Scalar value */
  @Function
  public Platform defaultPlatform() throws InterruptedException, ExecutionException, DaggerQueryException {
    return dag.defaultPlatform();
  }
}
