import sys

import anyio
import dagger

async def main():
    # initialize Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        # use NGINX container
        # add new webserver index page
        ctr = (
            client
            .container(platform=dagger.Platform("linux/amd64"))
            .from_("nginx:1.23-alpine")
            .with_new_file("/usr/share/nginx/html/index.html", "Hello from Dagger!", 0o400)
        )

        # export to host filesystem
        val = await ctr.export("/tmp/my-nginx.tar")

    # print result
    print(f"Exported image: {val}")

anyio.run(main)
