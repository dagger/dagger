import logging
import sys

import anyio

import dagger

WARNING_EXIT = 5
"""Exit code for warnings."""

REPORT_CMD = """
echo "QA Checks"
echo "========="
echo "Check 1: PASS"
echo "Check 2: FAIL"
echo "Check 3: PASS"
exit 1
"""


async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # Will only abort if there's an unexpected error,
        # in which case the next pipeline won't execute.
        await test(client)

        print(await report(client))


async def test(client: dagger.Client):
    try:
        await (
            client.container()
            .from_("alpine")
            .with_exec(["sh", "-c", "echo Skipped! >&2; exit 5"])
            .sync()
        )
    except dagger.ExecError as e:
        # Handle error from with_exec here, but let other errors bubble up.
        # Don't do anything when skipped.
        # Print message to stderr otherwise.
        if e.exit_code != WARNING_EXIT:
            print("Test failed:", e.stderr, file=sys.stderr)


async def report(client: dagger.Client) -> str:
    # Get stdout even on non-zero exit code.
    try:
        return await (
            client.container()
            .from_("alpines")  # ⚠️ typo! non-exec failure
            .with_exec(["sh", "-c", REPORT_CMD])
            .stdout()
        )
    except dagger.ExecError as e:
        # Not necessary to check for `e.exit_code != 0`.
        return e.stdout


if __name__ == "__main__":
    try:
        anyio.run(main)
    except dagger.DaggerError:
        # DaggerError is the base class for all errors raised by dagger.
        logging.exception("Unexpected dagger error")
        sys.exit(1)
