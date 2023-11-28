import { fct, object } from '@dagger.io/dagger'

/**
 * HelloWorld class
 */
@object
export class HelloWorld {
    @fct
    helloWorld(name: string): void {
        console.log(`hello ${name}`)
    }

    @fct
    async asyncHelloWorld(name?: string): Promise<void> {
        console.log(`async hello ${name}`)
    }
}