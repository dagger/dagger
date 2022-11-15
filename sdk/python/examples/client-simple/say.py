import sys

import anyio
from rich.console import Console

import dagger


async def main(args: list[str]):
    async with dagger.Connection() as client:
        # build container with cowsay entrypoint
        ctr = (
            client.container()
            .from_("python:alpine")
            .exec(["pip", "install", "cowsay"])
            .with_entrypoint(["cowsay"])
        )

        # run cowsay with requested message
        result = await ctr.exec(args).stdout().contents()

        print(result)


if __name__ == "__main__":
    if not sys.argv[1:]:
        print("What do you want to say?", file=sys.stderr)
        sys.exit(1)

    console = Console()
    with console.status("Hold on..."):
        anyio.run(main, sys.argv[1:])
