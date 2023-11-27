import { fct } from '@dagger.io/dagger'

/**
 * HelloWorld class
 * @object decorator is missing so this class should be ignored.
 */
export class Foo {
    @fct
    bar(name: string): string {
        return `hello ${name}`
    }
}