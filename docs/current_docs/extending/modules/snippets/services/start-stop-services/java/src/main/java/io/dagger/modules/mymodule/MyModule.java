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
  /** Explicitly start and stop a Redis service */
  @Function
  public String redisService()
      throws ExecutionException, DaggerQueryException, InterruptedException {
    Service redisSrv =
        dag().container()
            .from("redis")
            .withExposedPort(6379)
            .asService(new Container.AsServiceArguments().withUseEntrypoint(true));
    try {
      // start Redis ahead of time to it stays up for the duration of the test
      redisSrv = redisSrv.start();
      Container redisCli = dag().container().from("redis").withServiceBinding("redis-srv", redisSrv);
      List<String> args = List.of("redis-cli", "-h", "redis-srv");
      // set value
      String setter = redisCli.withExec(append(args, List.of("set", "foo", "bar"))).stdout();
      // get value
      String getter = redisCli.withExec(append(args, List.of("get", "foo"))).stdout();
      return setter + getter;
    } finally {
      redisSrv.stop();
    }
  }
  private List<String> append(List<String> list, List<String> elements) {
    return Stream.concat(list.stream(), elements.stream()).toList();
  }
}
