/**
 * Foo object module
 *
 * Compose of bar but its file description should be ignore.
 */
import { func, object } from "../../../decorators/decorators.ts"
import { Bar } from "./bar.ts"

/**
 * Foo class
 */
@object()
export class MultipleObjects {
  /**
   * Return Bar object
   */
  @func()
  bar(): Bar {
    return new Bar()
  }
}
