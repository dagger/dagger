import Client, { connect } from '@dagger.io/dagger';


connect(
  async (client: Client) => {
    // create Redis service container
    const redisSrv = client
      .container()
      .from('redis')
      .withExposedPort(6379)
      .withExec([]);

    // create Redis client container
    const redisCLI = client
      .container()
      .from('redis')
      .withServiceBinding('redis-srv', redisSrv)
      .withEntrypoint(['redis-cli', '-h', 'redis-srv']);

    // set and get value
    const val = await redisCLI
      .withExec(['set', 'foo', 'abc'])
      .withExec(['get', 'foo'])
      .stdout();

    console.log(val);
  },
  { LogOutput: process.stdout }
);
