package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Secret;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /**
   * Publish a container image to a private registry
   *
   * @param registry Registry address
   * @param username Registry username
   * @param password Registry password
   */
  @Function
  public String publish(String registry, String username, Secret password) throws ExecutionException, DaggerQueryException, InterruptedException {
    return dag()
        .container()
        .from("mginx:1.23-alpine")
        .withNewFile(
            "/usr/share/nginx/html/index.html",
            "Hello from Dagger!",
            new Container.WithNewFileArguments().withPermissions(0x400))
        .withRegistryAuth(registry, username, password)
        .publish("%s/%s/my-nginx".formatted(registry, username));
  }
}
