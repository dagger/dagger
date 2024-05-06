import { func, object } from '../../../decorators/decorators.js'
import { Platform } from '../../../../api/client.gen.js'

@object()
export class Scalar {
    @func()
    helloWorld(name: Platform): string {
        return `hello ${name}`
    }
}