package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;
import io.dagger.client.exception.DaggerQueryException;


import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public String getUser(String gender)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag().container()
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
