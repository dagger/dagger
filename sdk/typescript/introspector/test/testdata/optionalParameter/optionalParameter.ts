import { func, object } from '../../../decorators/decorators.js'

/**
 * HelloWorld class
 */
@object()
export class HelloWorld {
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
      b?: string,
      c: string = "foo",
      d: string | null = null,
      e: string | null = "bar",
    ): string {
      return [a, b, c, d, e].map(v => JSON.stringify(v)).join(", ")
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