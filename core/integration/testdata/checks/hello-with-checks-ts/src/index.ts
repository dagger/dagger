/**
 * A module for HelloWithChecksTs functions
 */
import { object, func, check } from "@dagger.io/dagger";
import { dag } from "./dagger/index.js";

@object()
class HelloWithChecksTs {
  /**
   * Returns a passing check
   */
  @func()
  @check()
  async passingCheck(): Promise<void> {
    await dag
      .container()
      .from("alpine:3")
      .withExec(["sh", "-c", "exit 0"])
      .sync();
  }

  /**
   * Returns a failing check
   */
  @func()
  @check()
  async failingCheck(): Promise<void> {
    await dag
      .container()
      .from("alpine:3")
      .withExec(["sh", "-c", "exit 1"])
      .sync();
  }
}
