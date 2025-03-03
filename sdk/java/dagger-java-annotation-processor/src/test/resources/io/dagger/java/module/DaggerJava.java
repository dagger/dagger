package io.dagger.java.module;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.*;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.Optional;
import java.util.concurrent.ExecutionException;

/** Dagger Java Module main object */
@Object
public class DaggerJava {
  private String notExportedField;

  /** Project source directory */
  public Directory source;

  public String version;

  public DaggerJava() {}

  /**
   * Initialize the DaggerJava Module
   *
   * @param source Project source directory
   * @param version Go version
   */
  public DaggerJava(Optional<Directory> source, @Default("1.23.2") String version) {
    this.source = source.orElseGet(() -> dag().currentModule().source());
    this.version = version;
  }

  /**
   * Returns a container that echoes whatever string argument is provided
   *
   * @param stringArg string to echo
   * @return container running echo
   */
  @Function
  public Container containerEcho(@Default("Hello Dagger") String stringArg) {
    return dag().container().from("alpine:latest").withExec(List.of("echo", stringArg));
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
      Optional<String> pattern)
      throws InterruptedException, ExecutionException, DaggerQueryException {
    String grepPattern = pattern.orElse("dagger");
    return dag()
        .container()
        .from("alpine:latest")
        .withMountedDirectory("/mnt", directoryArg)
        .withWorkdir("/mnt")
        .withExec(List.of("grep", "-R", grepPattern, "."))
        .stdout();
  }

  @Function
  public DaggerJava itself() {
    return this;
  }

  /**
   * Return true if the value is 0.
   *
   * <p>This description should not be exposed to dagger.
   */
  @Function(description = "but this description should be exposed")
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
  public String nullable(Optional<String> stringArg) {
    return stringArg.orElse("was a null value");
  }

  /** Set a default value in case the user doesn't provide a value and allow for null value. */
  @Function
  public String nullableDefault(@Default("Foo") Optional<String> stringArg) {
    return stringArg.orElse("was a null value by default");
  }

  /** return the default platform as a Scalar value */
  @Function
  public Platform defaultPlatform()
      throws InterruptedException, ExecutionException, DaggerQueryException {
    return dag().defaultPlatform();
  }

  @Function
  public float addFloat(float a, float b) {
    return a + b;
  }
}
