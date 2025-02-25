package io.dagger.modules.mymodule;

import io.dagger.client.DaggerQueryException;
import io.dagger.module.AbstractModule;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule extends AbstractModule {
  @Function
  public String getUser(String gender)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag.container()
        .from("alpine:latest")
        .withExec(List.of("apk", "add", "curl"))
        .withExec(List.of("apk", "add", "jq"))
        .withExec(
            List.of(
                "sh",
                "-c",
                "curl https://randomuser.me/api/?gender=%s | jq .results[0].name"
                    .formatted(gender)))
        .stdout();
  }
}
