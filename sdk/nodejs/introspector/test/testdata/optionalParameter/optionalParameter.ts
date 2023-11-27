import { fct, object } from '@dagger.io/dagger'

/**
 * HelloWorld class
 */
@object
export class HelloWorld {
    @fct
    helloWorld(name?: string): string {
        return `hello world ${name}`
    }

    @fct
    isTrue(value: boolean): boolean {
        return value
    }

    @fct
    add(a = 0, b = 0): number {
        return a + b
    }
}