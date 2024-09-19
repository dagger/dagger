/**
 * Should be ignored
 */
import { dag } from "../../../../api/client.gen.ts"
import { func, object } from "../../../decorators/decorators.ts"

/**
 * Bar class
 */
@object()
export class Bar {
  /**
   * Execute the command and return its result
   * @param cmd Command to execute
   */
  @func()
  async exec(cmd: string[]): Promise<string> {
    return await dag.container().from("alpine").withExec(cmd).stdout()
  }
}
