package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.Optional;

@Object
public class MyModule {
  /**
   * Return a container with a filtered directory
   *
   * @param source Source directory
   * @param excludeDirectoryPattern Directory exclusion pattern
   * @param excludeFilePattern File exclusion pattern
   */
  @Function
  public Container copyDirectoryWithExclusions(
      Directory source,
      Optional<String> excludeDirectoryPattern,
      Optional<String> excludeFilePattern) {
    Directory filteredSource = source;
    if (excludeDirectoryPattern.isPresent()) {
      filteredSource = filteredSource.withoutDirectory(excludeDirectoryPattern.get());
    }
    if (excludeFilePattern.isPresent()) {
      filteredSource = filteredSource.withoutFile(excludeFilePattern.get());
    }
    return dag().container().from("alpine:latest").withDirectory("/src", filteredSource);
  }
}
