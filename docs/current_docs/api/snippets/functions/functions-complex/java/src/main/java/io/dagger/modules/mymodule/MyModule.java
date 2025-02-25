package io.dagger.modules.mymodule;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.client.Directory;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

/** MyModule main object */
@Object
public class MyModule extends AbstractModule {
  @Function
  public string getUser() {
      return dag.container().from("alpine:latest")
      .withExec(List.of("apk", "add", "curl"))
      .withExec(List.of("apk", "add", "jq"))
      .withExec(List.of("sh", "-c", "curl https://randomuser.me/api/ | jq .results[0].name"))
      .stdout();
  }
}
