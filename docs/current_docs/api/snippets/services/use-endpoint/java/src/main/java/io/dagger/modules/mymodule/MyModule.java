package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.DaggerQueryException;
import io.dagger.client.Service;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  @Function
  public String get()
      throws ExecutionException, DaggerQueryException, InterruptedException {
    // Start NGINX service
    Service httpSrv =
        dag().container()
            .from("nginx")
            .withExposedPort(80)
            .asService();
    httpSrv = httpSrv.start();

    // Wait for service endpoint
    String ep = httpSrv.endpoint(new Service.EndpointArguments().withScheme("http").withPort(80));

    // Send HTTP request to service endpoint
    return dag().http(ep).contents();
  }
}
