package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.Service;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;
import java.util.stream.Stream;

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
    List<String> args = List.of("redis-cli", "-h", "redis-srv");
    // set and get value
    return redisCli
        .withExec(append(args, List.of("set", "foo", "bar")))
        .withExec(append(args, List.of("get", "foo")))
        .stdout();
  }
  private List<String> append(List<String> list, List<String> elements) {
    return Stream.concat(list.stream(), elements.stream()).toList();
  }
}
