import { func, object } from '../../../decorators/decorators.js'

@object()
export class Test {
    @func()
    echo(): string {
        return "world"
    }
}