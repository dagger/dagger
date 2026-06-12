/**
 * Test module, short description
 *
 * Long description, with full sentences.
 */
import { object, func } from "@dagger.io/dagger"

/**
 * Test object, short description
 */
@object()
export class Test {
  @func()
  foo: string = "foo"
}
