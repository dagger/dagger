package io.dagger.modules.workspace;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.client.CacheVolume;
import io.dagger.client.File;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class Workspace {
  /** the workspace source code */
  public Directory source;

  // Add a public no-argument constructor as required by the Java SDK
  public Workspace() {}

  public Workspace(Directory source) {
    this.source = source;
  }

  /**
   * Read a file in the Workspace
   *
   * @param path The path to the file in the workspace
   */
  @Function
  public String readFile(String path)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return source.file(path).contents();
  }

  /**
   * Write a file to the Workspace
   *
   * @param path The path to the file in the workspace
   * @param contents The new contents of the file
   */
  @Function
  public Workspace writeFile(String path, String contents) {
    this.source = source.withNewFile(path, contents);
    return this;
  }

  /**
   * List all of the files in the Workspace
   */
  @Function
  public String listFiles() throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag()
      .container()
      .from("alpine:3")
      .withDirectory("/src", source)
      .withWorkdir("/src")
      .withExec(List.of("tree", "/src"))
      .stdout();
  }

  /** Return the result of running unit tests */
  @Function
  public String test()
      throws InterruptedException, ExecutionException, DaggerQueryException {
    CacheVolume nodeCache = dag().cacheVolume("node");
    return dag().container()
        .from("node:21-slim")
        .withDirectory("/src", source)
        .withMountedCache("/root/.npm", nodeCache)
        .withWorkdir("/src")
        .withExec(List.of("npm", "install"))
        .withExec(List.of("npm", "run", "test:unit", "run"))
        .stdout();
  }

}
