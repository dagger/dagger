import { fct, object } from '@dagger.io/dagger'

/**
 * HelloWorld class
 */
@object
export class HelloWorld {
    private privateGreeting(name: string): string {
        return `Private hello ${name}`
    }

    @fct
    greeting(name: string): string {
        return this.privateGreeting(name)
    }

    @fct
    helloWorld(name: string): string {
        return `hello ${name}`
    }
}