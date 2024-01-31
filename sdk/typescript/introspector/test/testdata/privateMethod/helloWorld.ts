/**
 * HelloWorld module with private things
 */
import { func, object } from '../../../decorators/decorators.js'

/**
 * HelloWorld class
 */
@object()
export class HelloWorld {
    private privateGreeting(name: string): string {
        return `Private hello ${name}`
    }

    @func()
    greeting(name: string): string {
        return this.privateGreeting(name)
    }

    @func()
    helloWorld(name: string): string {
        return `hello ${name}`
    }
}