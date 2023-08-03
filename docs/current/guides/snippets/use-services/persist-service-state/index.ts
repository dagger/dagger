import { connect, Client } from "@dagger.io/dagger"

connect(
  async (client: Client) => {
    const redisSrv = client
      .container()
      .from("redis")
      .withExposedPort(6379)
      .withMountedCache("/data", client.cacheVolume("my-redis"))
      .withWorkdir("/data")

    // create Redis client container
    const redisCLI = client
      .container()
      .from("redis")
      .withServiceBinding("redis-srv", redisSrv)
      .withEntrypoint(["redis-cli", "-h", "redis-srv"])

    // set and save value
    await redisCLI.withExec(["set", "foo", "abc"]).withExec(["save"]).stdout()

    // get value
    const val = await redisCLI.withExec(["get", "foo"]).stdout()
    console.log(val)
  },
  { LogOutput: process.stderr }
)
