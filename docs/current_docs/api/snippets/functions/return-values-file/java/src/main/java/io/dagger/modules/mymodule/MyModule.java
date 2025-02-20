package io.dagger.modules.mymodule;

import io.dagger.client.Directory;
import io.dagger.client.File;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

@Object
public class MyModule extends AbstractModule {
  @Function
  public File archiver(Directory src) {
    return dag.container()
        .from("alpine:latest")
        .withExec(List.of("apk", "add", "zip"))
        .withMountedDirectory("/src", src)
        .withWorkdir("/src")
        .withExec(List.of("sh", "-c", "zip -p -r out.zip *.*"))
        .file("/src/out.zip");
  }
}
