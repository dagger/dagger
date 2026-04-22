import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Creates Redis service and client
   */
  @func()
  async redisService(): Promise<string> {
    const redisSrv = dag
      .container()
      .from("redis")
      .withExposedPort(6379)
      .asService({ useEntrypoint: true })

    // create Redis client container
    const redisCLI = dag
      .container()
      .from("redis")
      .withServiceBinding("redis-srv", redisSrv)
      .withEntrypoint()

    const args = ["redis-cli", "-h", "redis-srv"]

    // set value
    const setter = await redisCLI
      .withExec([...args, "set", "foo", "abc"])
      .stdout()

    // get value
    const getter = await redisCLI.withExec([...args, "get", "foo"]).stdout()

    return setter + getter
  }
}
