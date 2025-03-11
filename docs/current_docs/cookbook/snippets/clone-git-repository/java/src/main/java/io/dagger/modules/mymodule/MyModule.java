package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.Directory;
import io.dagger.client.GitRepository;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

@Object
public class MyModule {
  @Function
  public Container clone(String repository, String locator, String ref) {
    GitRepository r = dag().git(repository);
    Directory d =
        switch (locator) {
          case "BRANCH" -> r.branch(ref).tree();
          case "TAG" -> r.tag(ref).tree();
          case "COMMIT" -> r.commit(ref).tree();
          default -> throw new IllegalArgumentException("Invalid locator: " + locator);
        };

    return dag().container().from("alpine:latest").withDirectory("/src", d).withWorkdir("/src");
  }
}
