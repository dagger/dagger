package io.dagger.modules.viztestjava;

import static io.dagger.client.modules.core.Core.core;

import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.modules.core.Container;
import io.dagger.client.modules.sdkdep.SdkDep;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.List;

/**
 * The external Java SDK's fixture for the golden tests: every SDK fixture has the same functions,
 * so their goldens line up.
 */
@Object
public class ViztestJava {
  public ViztestJava() {}

  /** Logs to stdout and stderr, and succeeds. */
  @Function(cache = "session")
  public String logSuccess() throws Exception {
    logBoth();
    return "java function result";
  }

  /** Logs to stdout and stderr, then throws an error of its own. */
  @Function(cache = "session")
  public void failNative() throws Exception {
    logBoth();
    throw new Exception("java function failed natively");
  }

  /** Logs to stdout and stderr, then exits the process with a custom code. */
  @Function(cache = "session")
  public void failExitCode() throws Exception {
    logBoth();
    System.exit(3);
  }

  /** Runs a command that fails, and lets its error propagate. */
  @Function(cache = "session")
  public String failExec() throws Exception {
    return failingExec().stdout();
  }

  /** Runs a command that fails, and wraps its error. */
  @Function(cache = "session")
  public String failExecWrapped() throws Exception {
    try {
      return failingExec().stdout();
    } catch (DaggerQueryException e) {
      throw new Exception("java function ran a failing command: " + e.getMessage(), e);
    }
  }

  /** Calls a function of a dependency whose command fails. */
  @Function(cache = "session")
  public String callFailingDep() throws Exception {
    return SdkDep.sdkDep().failExec();
  }

  private static Container failingExec() {
    return core()
        .container()
        .from("alpine")
        .withExec(List.of("sh", "-c", "echo exec stdout; sleep 0.1; echo exec stderr >&2; exit 1"));
  }

  // Logs a line to stdout, then one to stderr. The streams are captured apart, so the pause keeps
  // their order stable in the goldens.
  private static void logBoth() throws InterruptedException {
    System.out.println("java function stdout");
    System.out.flush();
    Thread.sleep(100);
    System.err.println("java function stderr");
    System.err.flush();
  }
}
