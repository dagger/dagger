import sys

import anyio
import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # create HTTP service container with exposed port 8080
        httpSrv = (
            client.container()
            .from_("python")
            .with_directory(
                "/srv",
                client.directory().with_new_file("index.html", "Hello, world!"),
            )
            .with_workdir("/srv")
            .with_exec(["python", "-m", "http.server", "8080"])
            .with_exposed_port(8080)
        )

        # get endpoint
        val = await httpSrv.endpoint()

        # get HTTP endpoint
        val_scheme = await httpSrv.endpoint(scheme="http")

    print(val)
    print(val_scheme)


anyio.run(main)
