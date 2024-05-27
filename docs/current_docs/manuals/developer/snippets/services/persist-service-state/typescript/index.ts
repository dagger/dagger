import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Create Redis service and client
   */
  @func()
  async redisService(): Promise<string> {
    const redisSrv = dag
      .container()
      .from("redis")
      .withExposedPort(6379)
      .withMountedCache("/data", dag.cacheVolume("my-redis"))
      .withWorkdir("/data")
      .asService()

    // create Redis client container
    const redisCLI = dag
      .container()
      .from("redis")
      .withServiceBinding("redis-srv", redisSrv)
      .withEntrypoint(["redis-cli", "-h", "redis-srv"])

    // set and save value
    await redisCLI.withExec(["set", "foo", "abc"]).withExec(["save"]).sync()

    // get value
    return await redisCLI.withExec(["get", "foo"]).stdout()
  }
}
