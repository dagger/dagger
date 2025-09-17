package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.Service;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

@Object
public class MyModule {
  @Function
  public Service httpService() {
    return dag().container()
        .from("python")
        .withWorkdir("/srv")
        .withNewFile("index.html", "Hello world!")
        .withExposedPort(8080)
        .asService(
            new Container.AsServiceArguments()
                .withArgs(List.of("python", "-m", "http.server", "8080")));
  }
}
