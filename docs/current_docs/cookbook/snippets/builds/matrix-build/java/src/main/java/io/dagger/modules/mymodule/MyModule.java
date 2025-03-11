package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.*;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {

  private Container builder;

  /**
   * Build and return directory of go binaries
   *
   * @param src source code location
   */
  @Function
  public Directory build(Directory src)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    Directory outputs = dag().directory();
    Container golang =
        dag().container().from("golang:latest").withDirectory("/src", src).withWorkdir("/src");

    for (var goos : List.of("linux", "darwin")) {
      for (var goarch : List.of("amd64", "arm64")) {
        // create directory for each OS and architecture
        String path = "build/%s/%s/".formatted(goos, goarch);

        // build artifact
        Container build =
            golang
                .withEnvVariable("GOOS", goos)
                .withEnvVariable("GOARCH", goarch)
                .withExec(List.of("go", "build", "-o", path));

        // add build to outputs
        outputs = outputs.withDirectory(path, build.directory(path));
      }
    }

    return outputs;
  }
}
