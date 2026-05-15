/**
 * Not the main file
 */
import { object, func } from "@dagger.io/dagger"

@object()
export class Foo {
  @func()
  bar = "bar"
}
