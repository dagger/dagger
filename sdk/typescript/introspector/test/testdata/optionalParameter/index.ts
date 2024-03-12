import { func, object } from '../../../decorators/decorators.js'

/**
 * OptionalParameter class
 */
@object()
export class OptionalParameter {
    @func()
    helloWorld(name?: string): string {
        return `hello world ${name}`
    }

    @func()
    isTrue(value: boolean): boolean {
        return value
    }

    @func()
    add(a = 0, b = 0): number {
        return a + b
    }

    @func()
    sayBool(value = false): boolean {
        return value
    }

    @func()
    foo(
      a: string,
      b: string | null,
      c?: string,
      d: string = "foo",
      e: string | null = null,
      f: string | null = "bar",
    ): string {
      return [a, b, c, d, e, f].map(v => JSON.stringify(v)).join(", ")
    }
    
    @func()
    array(
        a: string[],
        b: (string | null)[],
        c: (string | null)[] | null,
    ): string {
        return [a, b, c].map(v => JSON.stringify(v)).join(", ")
    }
}