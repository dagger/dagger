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
      .asService()

    // create Redis client container
    const redisCLI = dag
      .container()
      .from("redis")
      .withServiceBinding("redis-srv", redisSrv)
      .withEntrypoint(["redis-cli", "-h", "redis-srv"])

    // send ping from client to server
    return await redisCLI.withExec(["ping"]).stdout()
  }
}
