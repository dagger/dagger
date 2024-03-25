import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async redisService(): Promise<string> {
    // create Redis service container
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
      .withEntrypoint(["redis-cli", "-h", "redis-srv"])

    // set value
    const setter = await redisCLI.withExec(["set", "foo", "abc"]).stdout()

    // get value
    const getter = await redisCLI.withExec(["get", "foo"]).stdout()

    return setter + getter
  }
}
