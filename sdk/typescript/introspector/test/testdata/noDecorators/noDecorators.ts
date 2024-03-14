import { func } from '../../../decorators/decorators.js'

/**
 * HelloWorld class
 * @object decorator is missing so this class should be ignored.
 */
export class NoDecorators {
    @func()
    bar(name: string): string {
        return `hello ${name}`
    }
}