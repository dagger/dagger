import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Explicitly start and stop a Redis service
   */
  @func()
  async redisService(): Promise<string> {
    let redisSrv = dag
      .container()
      .from("redis")
      .withExposedPort(6379)
      .asService()

    // start Redis ahead of time so it stays up for the duration of the test
    redisSrv = await redisSrv.start()

    // stop the service when done
    await redisSrv.stop()

    // create Redis client container
    const redisCLI = dag
      .container()
      .from("redis")
      .withServiceBinding("redis-srv", redisSrv)

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
