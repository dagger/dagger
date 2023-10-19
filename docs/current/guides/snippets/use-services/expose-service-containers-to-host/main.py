import sys

import anyio

import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # create HTTP service container with exposed port 8080
        http_srv = (
            client.container()
            .from_("python")
            .with_directory(
                "/srv",
                client.directory().with_new_file("index.html", "Hello, world!"),
            )
            .with_workdir("/srv")
            .with_exec(["python", "-m", "http.server", "8080"])
            .with_exposed_port(8080)
            .as_service()
        )

        # expose HTTP service to host
        tunnel = client.host().tunnel(http_srv).start()

        # get HTTP service address
        tunnel.endpoint()

        # access HTTP service from host
        # commenting below as it's a blocking function, needs changes


anyio.run(main)
