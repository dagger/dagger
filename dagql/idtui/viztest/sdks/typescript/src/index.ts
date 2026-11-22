// The external TypeScript SDK's fixture for the golden tests: every SDK
// fixture has the same functions, so their goldens line up.
import { dag, Container, object, func } from "@dagger.io/dagger"
import { sdkDep } from "@dagger.io/sdk-dep"

function failingExec(): Container {
  return dag
    .container()
    .from("alpine")
    .withExec(["sh", "-c", "echo exec stdout; sleep 0.1; echo exec stderr >&2; exit 1"])
}

// Logs a line to stdout, then one to stderr. The streams are captured apart,
// so the pause keeps their order stable in the goldens.
async function logBoth(): Promise<void> {
  console.log("typescript function stdout")
  await new Promise((resolve) => setTimeout(resolve, 100))
  console.error("typescript function stderr")
}

@object()
export class ViztestTypescript {
  /** Logs to stdout and stderr, and succeeds. */
  @func({ cache: "session" })
  async logSuccess(): Promise<string> {
    await logBoth()
    return "typescript function result"
  }

  /** Logs to stdout and stderr, then throws an error of its own. */
  @func({ cache: "session" })
  async failNative(): Promise<void> {
    await logBoth()
    throw new Error("typescript function failed natively")
  }

  /** Logs to stdout and stderr, then exits the process with a custom code. */
  @func({ cache: "session" })
  async failExitCode(): Promise<void> {
    await logBoth()
    process.exit(3)
  }

  /** Runs a command that fails, and lets its error propagate. */
  @func({ cache: "session" })
  async failExec(): Promise<string> {
    return failingExec().stdout()
  }

  /** Runs a command that fails, and wraps its error. */
  @func({ cache: "session" })
  async failExecWrapped(): Promise<string> {
    try {
      return await failingExec().stdout()
    } catch (e) {
      // The module's "@dagger.io/dagger" doesn't export ExecError to test
      // for, so any error is wrapped.
      throw new Error(`typescript function ran a failing command: ${(e as Error).message}`)
    }
  }

  /** Logs a line, then reads a file that doesn't exist, and lets the error propagate. */
  @func({ cache: "session" })
  async failApi(): Promise<string> {
    console.log("typescript function stdout")
    return dag.directory().file("doesnt-exist.txt").contents()
  }

  /** Calls a function of a dependency whose command fails. */
  @func({ cache: "session" })
  async callFailingDep(): Promise<string> {
    return sdkDep().failExec()
  }

  /** Logs a line, then calls a function of a dependency whose command fails. */
  @func({ cache: "session" })
  async callFailingDepLogged(): Promise<string> {
    console.log("typescript function stdout")
    return sdkDep().failExec()
  }
}
