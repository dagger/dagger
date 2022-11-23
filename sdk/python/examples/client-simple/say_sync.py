import sys

from rich.console import Console

import dagger


def main(args: list[str]):
    with dagger.Connection() as client:
        # build container with cowsay entrypoint
        ctr = (
            client.container()
            .from_("python:alpine")
            .with_exec(["pip", "install", "cowsay"])
            .with_entrypoint(["cowsay"])
        )

        # run cowsay with requested message
        result = ctr.with_exec(args).stdout()

        print(result)


if __name__ == "__main__":
    if not sys.argv[1:]:
        print("What do you want to say?", file=sys.stderr)
        sys.exit(1)

    console = Console()
    with console.status("Hold on..."):
        main(sys.argv[1:])
