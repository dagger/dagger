import Client, { connect } from '@dagger.io/dagger';


connect(
  async (client: Client) => {
    // get hostname of service container
    const val = await client
      .container()
      .from('alpine')
      .withExec(['hostname'])
      .stdout();

    console.log(val);
  },
  { LogOutput: process.stdout }
);
