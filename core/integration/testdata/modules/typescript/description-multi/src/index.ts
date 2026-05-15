/**
 * Test module, short description
 *
 * Long description, with full sentences.
 */
import { object, func } from "@dagger.io/dagger"
import { Foo } from "./foo"

/**
 * Test object, short description
 */
@object()
export class Test {
  @func()
  foo(): Foo {
    return new Foo()
  }
}
