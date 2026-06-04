package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.Directory;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

@Object
public class MyModule {
  @Function
  public Container foo(Directory source) {
    Container builder =
        dag()
            .container()
            .from("golang:latest")
            .withDirectory(
                "/src",
                source,
                new Container.WithDirectoryArguments().withExclude(List.of("*.git", "internal")))
            .withWorkdir("/src/hello")
            .withExec(List.of("go", "build", "-o", "hello.bin", "."));

    return dag()
        .container()
        .from("alpine:latest")
        .withDirectory(
            "/app",
            builder.directory("/src/hello"),
            new Container.WithDirectoryArguments().withInclude(List.of("hello.bin")))
        .withEntrypoint(List.of("/app/hello.bin"));
  }
}
