import dagger
from dagger import DaggerError, dag, field, function, object_type

SCRIPT = """#!/bin/sh
echo "Test Suite"
echo "=========="
echo "Test 1: PASS" >> report.txt
echo "Test 2: FAIL" >> report.txt
echo "Test 3: PASS" >> report.txt
exit 1
"""


@object_type
class TestResult:
    report: dagger.File = field()
    exit_code: str = field()


@object_type
class MyModule:
    @function
    async def test(self) -> TestResult:
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

            # save report for inspection.
            report = ctr.file("report.txt")

            # use the saved exit code to determine if the tests passed.
            exit_code = await ctr.file("exit_code").contents()

            return TestResult(report=report, exit_code=exit_code)
        except DaggerError:
            # DaggerError is the base class for all errors raised by Dagger
            raise "Unexpected Dagger error"


# ruff: noqa: RET505
