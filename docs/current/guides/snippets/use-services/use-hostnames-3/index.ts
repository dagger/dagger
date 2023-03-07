import Client, { connect } from '@dagger.io/dagger';

connect(
  async (client: Client) => {
    // get hostname of service container
    const val = await client
      .container()
      .from('node:slim')
      .withExec(['npx', 'http-server', '-p', '8080'])
      .hostname()

    console.log(val);
  },
  { LogOutput: process.stdout }
);
