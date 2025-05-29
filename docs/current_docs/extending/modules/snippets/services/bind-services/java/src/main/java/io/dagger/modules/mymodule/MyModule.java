package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Service;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;
 @Objectic class MyModule {ion Service httpService() {g().container().from("pyt         .withNewFile("index.html", "Hello, world!")
        .withExposedPort(8080)
        .asService(
            new Container.AsServiceArguments()
                .withArgs(List.of("python", "-m", "http.server", "8080")));
  }

  @Function
  public String get() throws ExecutionException, DaggerExecException, DaggerQueryException, InterruptedException {
    return dag().container()
        .from("alpine")
        .withServiceBinding("www", httpService())
        .withExec(List.of("wget", "-O-", "http://www:8080"))
        .stdout();
  }
}
