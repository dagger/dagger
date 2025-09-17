package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Secret;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public String githubApi(Secret token) throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag().container()
        .from("alpine:3.17")
        .withSecretVariable("GITHUB_API_TOKEN", token)
        .withExec(List.of("apk", "add", "curl"))
        .withExec(
            List.of(
                "sh",
                "-c",
                "curl \"https://api.github.com/repos/dagger/dagger/issues\""
                    + " --header \"Authorization: Bearer $GITHUB_API_TOKEN\""
                    + " --header \"Accept: application/vnd.github+json\""))
        .stdout();
  }
}
