package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Service;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {

  /** creates Redis service and client */
  @Function
  public String redisService()
      throws ExecutionException, DaggerQueryException, InterruptedException {
    Service redisSrv =
        dag().container()
            .from("redis")
            .withExposedPort(6379)
            .asService(new Container.AsServiceArguments().withUseEntrypoint(true));
    // create Redis client container
    Container redisCli = dag().container().from("redis").withServiceBinding("redis-srv", redisSrv);
    // send ping from client to server
    return redisCli.withExec(List.of("redis-cli", "-h", "redis-srv", "ping")).stdout();
  }
}
