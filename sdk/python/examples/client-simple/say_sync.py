import sys

import dagger


def main(args: list[str]):
    # Tip: If you want to see the output from the engine use
    # `dagger.Connection(dagger.Config(log_output=sys.stderr))`
    with dagger.Connection() as client:
        # build container with cowsay entrypoint
        # note: this is reusable, no request is made to the server
        ctr = (
            client.container()
            .from_("python:alpine")
            .exec(["pip", "install", "cowsay"])
            .with_entrypoint(["cowsay"])
        )

        # run cowsay with requested message
        # note: methods that return a coroutine need to await query execution
        result = ctr.exec(args).stdout().contents()

        print(result)


if __name__ == "__main__":
    if not sys.argv[1:]:
        print("What do you want to say?", file=sys.stderr)
        sys.exit(1)
    main(sys.argv[1:])
