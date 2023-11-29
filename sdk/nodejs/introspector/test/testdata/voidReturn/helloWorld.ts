import { func, object } from '@dagger.io/dagger'

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