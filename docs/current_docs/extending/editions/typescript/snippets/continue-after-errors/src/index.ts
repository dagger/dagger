import { dag, object, func, File, ReturnType } from "@dagger.io/dagger"

const SCRIPT = `#!/bin/sh
echo "Test Suite"
echo "=========="
echo "Test 1: PASS" | tee -a report.txt
echo "Test 2: FAIL" | tee -a report.txt
echo "Test 3: PASS" | tee -a report.txt
exit 1
`

@object()
class TestResult {
  @func()
  report: File

  @func()
  exitCode: number
}

@object()
class MyModule {
  /**
   * Handle errors
   */
  @func()
  async test(): Promise<TestResult> {
    const ctr = await dag
      .container()
      .from("alpine")
      // add script with execution permission to simulate a testing tool.
      .withNewFile("/run-tests", SCRIPT, { permissions: 0o750 })
      // run-tests but allow any return code
      .withExec(["/run-tests"], { expect: ReturnType.Any })
      // the result of `sync` is the container, which allows continued chaining
      .sync()

    const result = new TestResult()
    // save report for inspection.
    result.report = ctr.file("report.txt")

    // use the saved exit code to determine if the tests passed
    result.exitCode = await ctr.exitCode()

    return result
  }
}
