from dagger import dag, DaggerError, function, object_type


SCRIPT = """#!/bin/sh
echo "Test Suite"
echo "=========="
echo "Test 1: PASS" >> report.txt
echo "Test 2: FAIL" >> report.txt
echo "Test 3: PASS" >> report.txt
exit 1
"""


@object_type
class MyModule:
    @function
    async def test(self) -> str:
        """Handle errors"""
        try:
            ctr = (
                await (
                    dag.container().from_("alpine")
                    # add script with execution permission to simulate a testing tool.
                    .with_new_file("run-tests", contents=SCRIPT, permissions=0o750)
                    # if the exit code isn't needed: "run-tests; true"
                    .with_exec(["sh", "-c", "/run-tests; echo -n $? > /exit_code"])
                    # the result of `sync` is the container, which allows continued chaining
                    .sync()
                )
            )

            # save report locally for inspection.
            await ctr.file("report.txt").export("report.txt")

            # use the saved exit code to determine if the tests passed.
            exit_code = await ctr.file("exit_code").contents()

            if exit_code != "0":
                return "Tests failed!"
            return "Tests passed!"
        except DaggerError:
            # DaggerError is the base class for all errors raised by Dagger
            return "Unexpected Dagger error"
