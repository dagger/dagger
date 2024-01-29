import { func, object } from '../../../decorators/decorators.js'

/**
 * HelloWorld class
 */
@object
export class HelloWorld {
    @func
    helloWorld(name: string): void {
        console.log(`hello ${name}`)
    }

    @func
    async asyncHelloWorld(name?: string): Promise<void> {
        console.log(`async hello ${name}`)
    }
}