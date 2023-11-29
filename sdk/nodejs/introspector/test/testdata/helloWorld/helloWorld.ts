import { func, object } from '@dagger.io/dagger'

/**
 * HelloWorld class
 */
@object
export class HelloWorld {
    @func
    helloWorld(name: string): string {
        return `hello ${name}`
    }
}