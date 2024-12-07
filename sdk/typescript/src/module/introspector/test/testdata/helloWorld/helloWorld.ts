import { func, object } from "../../../../decorators.js"

/**
 * HelloWorld class
 */
@object()
export class HelloWorld {
  @func()
  helloWorld(name: string): string {
    return `hello ${name}`
  }
}
