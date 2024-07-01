import { dag, object, func, File } from "@dagger.io/dagger"

const SCRIPT = `#!/bin/sh
echo "Test Suite"
echo "=========="
echo "Test 1: PASS" >> report.txt
echo "Test 2: FAIL" >> report.txt
echo "Test 3: PASS" >> report.txt
exit 1
`

@object()
class TestResult {
  @func()
  report: File

  @func()
  exitCode: string
}

@object()
class MyModule {
  /**
   * Handle errors
   */
  @func()
  async test(): Promise<TestResult> {
    try {
      const ctr = await dag
        .container()
        .from("alpine")
        // add script with execution permission to simulate a testing tool.
        .withNewFile("run-tests", { contents: SCRIPT, permissions: 0o750 })
        // if the exit code isn't needed: "run-tests; true
        .withExec(["sh", "-c", "/run-tests; echo -n $? > /exit_code"])
        // the result of `sync` is the container, which allows continued chaining
        .sync()

      const result = new TestResult()
      // save report for inspection.
      result.report = ctr.file("report.txt")

      // use the saved exit code to determine if the tests passed
      result.exitCode = await ctr.file("exit_code").contents()

      return result
    } catch (e) {
      console.error(e)
    }
  }
}
