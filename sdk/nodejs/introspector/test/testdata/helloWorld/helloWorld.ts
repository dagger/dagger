import { fct, object } from '@dagger.io/dagger'

/**
 * HelloWorld class
 */
@object
export class HelloWorld {
    @fct
    helloWorld(name: string): string {
        return `hello ${name}`
    }
}