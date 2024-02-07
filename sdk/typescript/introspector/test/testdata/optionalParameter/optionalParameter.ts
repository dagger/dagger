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
}