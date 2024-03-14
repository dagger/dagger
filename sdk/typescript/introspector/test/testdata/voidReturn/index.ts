import { func, object } from '../../../decorators/decorators.js'

/**
 * VoidReturn class
 */
@object()
export class VoidReturn {
    @func()
    helloWorld(name: string): void {
        console.log(`hello ${name}`)
    }

    @func()
    async asyncHelloWorld(name?: string): Promise<void> {
        console.log(`async hello ${name}`)
    }
}