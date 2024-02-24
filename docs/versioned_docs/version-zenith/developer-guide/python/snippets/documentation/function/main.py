TODO
import { object, func } from '@dagger.io/dagger';

@object()
class MyModule {
  /**
   * Compute the sum of two numbers.
   *
   * Example usage: dagger call add --a=4 --b=5
   *
   * @param a The first number to add
   * @param b The second number to add
   */
  @func()
  add(a: number = 4, b: number): number {
    return a + b
  }
}
