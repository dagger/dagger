package io.dagger.modules.mymodule;

import static io.dagger.client.Dagger.dag;

import io.dagger.client.Container;
import io.dagger.client.ReturnType;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;
import java.util.concurrent.ExecutionException;

@Object
public class MyModule {
  private final String script =
"""
#!/bin/sh
echo "Test Suite"
echo "=========="
echo "Test 1: PASS" | tee -a report.txt
echo "Test 2: FAIL" | tee -a report.txt
echo "Test 3: PASS" | tee -a report.txt
exit 1
""";

  /** Handle errors */
  @Function
  public TestResult test() throws ExecutionException, DaggerQueryException, InterruptedException {
    var ctr =
        dag()
            .container()
            .from("alpine")
            // add script with execution permission to simulate a testing tool
            .withNewFile(
                "/run-tests", script, new Container.WithNewFileArguments().withPermissions(0750))
            // run-tests but allow any return code
            .withExec(
                List.of("/run-tests"), new Container.WithExecArguments().withExpect(ReturnType.ANY))
            // the result of `sync` is the container, which allows continued chaining
            .sync();

    // save report for inspection
    var report = ctr.file("report.txt");

    // use the saved exit code to determine if the tests passed
    var exitCode = ctr.exitCode();

    return new TestResult(report, exitCode);
  }
}
