import Client, { connect } from '@dagger.io/dagger';

connect(
  async (client: Client) => {
    // get IP address of service container
    const val = await client
      .container()
      .from('alpine')
      .withExec(['sh', '-c', 'ip route | grep src'])
      .stdout();
    console.log(val);
  },
  { LogOutput: process.stdout }
);
