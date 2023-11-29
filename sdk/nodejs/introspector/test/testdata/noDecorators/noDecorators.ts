import { func } from '@dagger.io/dagger'

/**
 * HelloWorld class
 * @object decorator is missing so this class should be ignored.
 */
export class Foo {
    @func
    bar(name: string): string {
        return `hello ${name}`
    }
}