import { dag, object, func, Container } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Create Redis service and client
   */
  @func()
  redis(): Container {
    const redisSrv = dag
      .container()
      .from("redis")
      .withExposedPort(6379)
      .withMountedCache("/data", dag.cacheVolume("my-redis"))
      .withWorkdir("/data")
      .asService()

    const redisCLI = dag
      .container()
      .from("redis")
      .withServiceBinding("redis-srv", redisSrv)
      .withEntrypoint(["redis-cli", "-h", "redis-srv"])

    return redisCLI
  }

  /**
   * Set key and value in Redis service
   */
  @func()
  async set(
    /**
     * The cache key to set
     */
    key: string,
    /**
     * The cache value to set
     */
    value: string,
  ): Promise<string> {
    return await this.redis()
      .withExec(["set", key, value])
      .withExec(["save"])
      .stdout()
  }

  /**
   * Get value from Redis service
   */
  @func()
  async get(
    /**
     * The cache key to get
     */
    key: string,
  ): Promise<string> {
    // set and save value
    return await this.redis().withExec(["get", key]).stdout()
  }
}
