package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Directory;
import io.dagger.client.File;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

@Object
public class MyModule {
  @Function
  public File build(Directory src, String arch, String os) {
    return dag().container()
        .from("golang:1.21")
        .withMountedDirectory("/src", src)
        .withWorkdir("/src")
        .withEnvVariable("GOARCH", arch)
        .withEnvVariable("GOOS", os)
        .withEnvVariable("CGO_ENABLED", "0")
        .withExec(List.of("go", "build", "-o", "build/"))
        .file("/src/build/hello");
  }
}
