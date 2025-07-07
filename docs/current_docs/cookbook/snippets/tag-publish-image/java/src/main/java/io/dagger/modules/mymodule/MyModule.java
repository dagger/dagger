package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Secret;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  /**
   * Publish a container image multiple times and publish it to a private registry.
   *
   * @param registry Registry address
   * @param username Registry username
   * @param password Registry password
   */
  @Function
  public List<String> publish(String registry, String username, Secret password)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    List<String> tags = List.of("latest", "1.0-alpine", "1.0", "1.0.0");
    List<String> addr = new ArrayList<>();
    Container ctr =
        dag()
            .container()
            .from("mginx:1.23-alpine")
            .withNewFile(
                "/usr/share/nginx/html/index.html",
                "Hello from Dagger!",
                new Container.WithNewFileArguments().withPermissions(0x400))
            .withRegistryAuth(registry, username, password);
    for (String tag : tags) {
      String a = ctr.publish("%s/%s/my-nginx:%s".formatted(registry, username, tag));
      addr.add(a);
    }
    return addr;
  }
}
