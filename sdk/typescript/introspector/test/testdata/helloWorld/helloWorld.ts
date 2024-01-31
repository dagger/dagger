import { func, object } from '../../../decorators/decorators.js'

/**
 * HelloWorld class
 */
@object()
export class HelloWorld {
    @func()
    helloWorld(name: string): string {
        return `hello ${name}`
    }
}