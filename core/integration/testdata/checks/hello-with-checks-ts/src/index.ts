/**
 * A module for HelloWithChecksTs functions
 */
import { dag, object, func, check } from "@dagger.io/dagger";

@object()
class HelloWithChecksTs {
  @func()
  baseImage: string;

  constructor(baseImage: string = "alpine:3") {
    this.baseImage = baseImage;
  }

  /**
   * Returns a passing check
   */
  @func()
  @check()
  async passingCheck(): Promise<void> {
    await dag
      .container()
      .from(this.baseImage)
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
      .from(this.baseImage)
      .withExec(["sh", "-c", "exit 1"])
      .sync();
  }
}
