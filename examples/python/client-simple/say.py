import sys
import anyio

import dagger


async def main(args: list[str]):
    async with dagger.Connection() as client:
        # build container with cowsay entrypoint
        # note: this is reusable, no request is made to the server
        ctr = (
            client.container()
            .from_("python:alpine")
            .exec(["pip", "install", "cowsay"])
            .with_entrypoint(["cowsay"])
        )

        # run cowsay with requested message
        # note: methods that return a coroutine with a Result need to await query execution
        result = await ctr.exec(args).stdout().contents()

        print(result)


if __name__ == "__main__":
    anyio.run(main, sys.argv[1:])
