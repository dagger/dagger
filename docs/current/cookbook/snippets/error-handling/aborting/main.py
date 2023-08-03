import logging
import sys

import anyio

import dagger


async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        try:
            await test(client)
        except dagger.QueryError as e:
            # QueryError is for valid GraphQL responses that return errors.
            print(e, file=sys.stderr)
            # Abort script with non-zero exit code.
            sys.exit(1)

        print("Test passed!")


async def test(client: dagger.Client):
    await (
        client.container()
        .from_("alpine")
        # ERROR: cat: read error: Is a directory
        .with_exec(["cat", "/"])
        .sync()
    )


if __name__ == "__main__":
    try:
        anyio.run(main)
    except dagger.DaggerError:
        # DaggerError is the base class for all errors raised by dagger.
        logging.exception("Unexpected dagger error")
        sys.exit(1)
