/**
 * Constructor module
 */
import { func, object } from "../../../../decorators/index.js"

/**
 * Constructor class
 */
@object()
export class Constructor {
  name: string

  constructor(name: string = "world") {
    this.name = name
  }

  @func()
  sayHello(name: string): string {
    return `hello ${name}`
  }
}
