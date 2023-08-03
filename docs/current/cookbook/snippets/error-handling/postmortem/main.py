import logging
import sys

import anyio

import dagger

SCRIPT = """#!/bin/sh
echo "Test Suite"
echo "=========="
echo "Test 1: PASS" >> report.txt
echo "Test 2: FAIL" >> report.txt
echo "Test 3: PASS" >> report.txt
exit 1
"""


async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        await test(client)


async def test(client: dagger.Client):
    # If any one of these steps fails, it's an unexpected error so we don't
    # need to handle anything here.

    # The result of `sync` is the container, which allows continued chaining.
    ctr = await (
        client.container()
        .from_("alpine")
        # Add script with execution permission to simulate a testing tool.
        .with_new_file("run-tests", contents=SCRIPT, permissions=0o750)
        # If the exit code isn't needed: "run-tests; true"
        .with_exec(["sh", "-c", "/run-tests; echo -n $? > /exit_code"])
        .sync()
    )

    # Save report locally for inspection.
    await ctr.file("report.txt").export("report.txt")

    # Use the saved exit code to determine if the tests passed.
    exit_code = await ctr.file("exit_code").contents()

    if exit_code != "0":
        print("Tests failed!", file=sys.stderr)
    else:
        print("Tests passed!")


if __name__ == "__main__":
    try:
        anyio.run(main)
    except dagger.DaggerError:
        # DaggerError is the base class for all errors raised by dagger.
        logging.exception("Unexpected dagger error")
        sys.exit(1)
