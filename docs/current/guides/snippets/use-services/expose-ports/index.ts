import Client, { connect } from '@dagger.io/dagger';

connect(
  async (client: Client) => {
    const httpSrv = client
      .container()
      .from('node:slim')
      .withDirectory(
        '/srv',
        client.directory().withNewFile('index.html', 'Hello, world!')
      )
      .withWorkdir('/srv')
      .withExec(['npx', 'http-server', '-p', '8080'])
      .withExposedPort(8080);

    const val = await httpSrv.endpoint({ scheme: 'http' });

    console.log(val);
  },
  { LogOutput: process.stdout }
);

