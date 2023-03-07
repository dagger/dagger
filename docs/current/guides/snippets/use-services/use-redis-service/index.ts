import Client, { connect } from '@dagger.io/dagger';

connect(
  async (client: Client) => {

    if (process.argv.length < 4) {
      throw Error(`usage: ${process.argv[0]} <cache-key> <command ...>`)
    }

    const key = process.argv[2]
    const cmd = process.argv.slice(3)

    // get hostname of service container
    const redis = client
      .container()
      .from('redis')

    // create a Redis service with a persistent cache
    const redisSrv = redis
      .withExposedPort(6379)
      .withMountedCache("data", client.cacheVolume(key))
      .withWorkdir("/data")
      .withExec([])

    // create a redis-cli container that runs against the service
    const redisCLI = redis
      .withServiceBinding('redis-srv', redisSrv)
      .withEntrypoint(['redis-cli', '-h', 'redis-srv']);

    // create the execution plan for the user's command
    // avoid caching via an environment variable
    const redisCmd = redisCLI
      .withEnvVariable("AT", Date.now().toString())
      .withExec(cmd);

    // first: run the command and immediately save
    await redisCmd.withExec(["save"]).exitCode();

    // then: print the output of the (cached) command
    const out = await redisCmd.stdout()

    console.log(out);
  },
  { LogOutput: process.stdout }
);
