/**
 * Constructor module
 */
import { func, object } from '../../../decorators/decorators.js'

/**
 * HelloWorld class
 */
@object()
export class HelloWorld {
    name: string

    constructor(name: string = "world") {
        this.name = name
    }

    @func()
    sayHello(name: string): string {
        return `hello ${name}`
    }
}