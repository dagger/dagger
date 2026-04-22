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

  private Container.WithExecArguments execOpts =
      new Container.WithExecArguments().withUseEntrypoint(true);

  /** Create Redis service and client */
  @Function
  public Container redis() {
    Service redisSrv =
        dag().container()
            .from("redis")
            .withExposedPort(6379)
            .withMountedCache("/data", dag().cacheVolume("my-redis"))
            .withWorkdir("/data")
            .asService(new Container.AsServiceArguments().withUseEntrypoint(true));
    Container redisCli =
        dag().container()
            .from("redis")
            .withServiceBinding("redis-srv", redisSrv)
            .withEntrypoint(List.of("redis-cli", "-h", "redis-srv"));
    return redisCli;
  }
  /**
   * Set key and value in Redis service
   *
   * @param key The cache key to set
   * @param value The cache value to set
   */
  @Function
  public String set(String key, String value)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return redis()
        .withExec(List.of("set", key, value), execOpts)
        .withExec(List.of("save"), execOpts)
        .stdout();
  }
  /**
   * Get value from Redis service
   *
   * @param key The cache key to get
   */
  @Function
  public String get(String key)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    return redis().withExec(List.of("get", key), execOpts).stdout();
  }
}
