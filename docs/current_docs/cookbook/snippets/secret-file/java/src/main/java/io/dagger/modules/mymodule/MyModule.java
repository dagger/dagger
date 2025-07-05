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
  /**
   * Query the GitHub API
   *
   * @param ghCreds GitHub Hosts configuration file
   */
  @Function
  public String githubAuth(Secret ghCreds)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag()
        .container()
        .from("alpine:3.17")
        .withExec(List.of("apk", "add", "github-cli"))
        .withMountedSecret("/root/.config/gh/hosts.yml", ghCreds)
        .withWorkdir("/root")
        .withExec(List.of("gh", "auth", "status"))
        .stdout();
  }
}
