import sys

import anyio

import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # expose host service on port 3306
        host_srv = client.host().service([dagger.PortForward(frontend=3306, backend=3306, protocol='TCP')])

        # create MariaDB container
        # with host service binding
        # execute SQL query on host service
        out = await (
            client.container()
            .from_("mariadb:10.11.2")
            .with_service_binding("db", host_srv)
            .with_exec(
                [
                    "/bin/sh",
                    "-c",
                    "/usr/bin/mysql --user=root --password=secret --host=db -e 'SELECT * FROM mysql.user'",
                ]
            )
            .stdout()
        )

    print(out)


anyio.run(main)
