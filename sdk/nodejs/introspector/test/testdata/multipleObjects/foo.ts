import { func, object } from '@dagger.io/dagger'

import { Bar } from './bar.js'


/**
 * Foo class
 */
@object
export class Foo {
    /**
     * Return Bar object
     */
    @func
    bar(): Bar {
        return new Bar()
    }
}