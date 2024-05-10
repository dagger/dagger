/**
 * A simple example module to say hello.
 *
 * Further documentation for the module here.
 */

import { object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Return a greeting.
   *
   * @param name Who to greet
   * @param greeting The greeting to display
   */
  @func()
  hello(name: string, greeting: string): string {
    return `${greeting}, ${name}!`
  }

  /**
   * Return a loud greeting.
   *
   * @param name Who to greet
   * @param greeting The greeting to display
   */
  @func()
  loudHello(name: string, greeting: string): string {
    return `${greeting.toUpperCase()}, ${name.toUpperCase()}!`
  }
}
