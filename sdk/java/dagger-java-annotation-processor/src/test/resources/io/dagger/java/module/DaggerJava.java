package io.dagger.java.module;

import static io.dagger.client.Dagger.dag;
import java.util.List;
import java.util.Optional;
import java.util.concurrent.ExecutionException;
import io.dagger.client.Container;
import io.dagger.client.Directory;
import io.dagger.client.Platform;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Check;
import io.dagger.module.annotation.Default;
import io.dagger.module.annotation.DefaultPath;
import io.dagger.module.annotation.Enum;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Ignore;
import io.dagger.module.annotation.Object;

/** Dagger Java Module main object */
@Object
public class DaggerJava {
  /** Severities */
  @Enum
  public enum Severity {
    /** Debug severity */
    DEBUG,
    /** Info severity */
    INFO,
    WARN,
    ERROR,
    FATAL,
  }

  private transient String notExportedField;

  /** Project source directory */
  public Directory source;

  // this field will also be exposed as a Dagger Field, even if private
  @Function private String version;

  // this field will be serialized but not exposed as a field
  private Container container;

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

  /** Function returning nothing */
  @Function
  public void doSomething(Directory src) {
    // do something
  }

  @Function
  public String printSeverity(Severity severity) {
    return severity.name();
  }

  /** Validates the module configuration */
  @Function
  @Check
  public void validate() {
    // Validation logic - throws exception on failure
    if (version == null || version.isEmpty()) {
      throw new IllegalStateException("Version must be set");
    }
  }
}
