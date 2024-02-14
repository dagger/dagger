import { connect, Client } from "@dagger.io/dagger"

connect(
  async (client: Client) => {
    // expose host service on port 3306
    const hostSrv = client.host().service([{ frontend: 3306, backend: 3306 }])

    // create MariaDB container
    // with host service binding
    // execute SQL query on host service
    const out = await client
      .container()
      .from("mariadb:10.11.2")
      .withServiceBinding("db", hostSrv)
      .withExec([
        "/bin/sh",
        "-c",
        "/usr/bin/mysql --user=root --password=secret --host=db -e 'SELECT * FROM mysql.user'",
      ])
      .stdout()

    console.log(out)
  },
  { LogOutput: process.stderr },
)
