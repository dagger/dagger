import dagger
from dagger import DaggerError, dag, field, function, object_type

SCRIPT = """#!/bin/sh
echo "Test Suite"
echo "=========="
echo "Test 1: PASS" | tee -a report.txt
echo "Test 2: FAIL" | tee -a report.txt
echo "Test 3: PASS" | tee -a report.txt
exit 1
"""


@object_type
class TestResult:
    report: dagger.File = field()
    exit_code: int = field()


@object_type
class MyModule:
    @function
    async def test(self) -> TestResult:
        """Handle errors"""
        try:
            ctr = await (
                dag.container()
                .from_("alpine")
                # add script with execution permission to simulate a testing tool.
                .with_new_file("/run-tests", SCRIPT, permissions=0o750)
                # run-tests but allow any return code
                .with_exec(["/run-tests"], expect=dagger.ReturnType.ANY)
                # the result of `sync` is the container, which allows continued chaining
                .sync()
            )

            # save report for inspection.
            report = ctr.file("report.txt")

            # use the saved exit code to determine if the tests passed.
            exit_code = await ctr.exit_code()

            return TestResult(report=report, exit_code=exit_code)
        except DaggerError as e:
            # DaggerError is the base class for all errors raised by Dagger
            msg = "Unexpected Dagger error"
            raise RuntimeError(msg) from e


# ruff: noqa: RET505
