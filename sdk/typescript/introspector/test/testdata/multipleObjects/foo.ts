/**
 * Foo object module
 * 
 * Compose of bar but its file description should be ignore.
 */
import { func, object } from '../../../decorators/decorators.js'

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