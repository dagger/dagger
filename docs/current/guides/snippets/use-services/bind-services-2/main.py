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
        )

        # create client container with service binding
        # access HTTP service, write to file and retrieve contents
        val = await (
            client.container()
            .from_("alpine")
            .with_service_binding("www", http_srv)
            .with_exec(["wget", "http://www:8080"])
            .file("index.html")
            .contents()
        )

    print(val)


anyio.run(main)
