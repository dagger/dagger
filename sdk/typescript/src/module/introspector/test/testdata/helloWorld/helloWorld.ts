import { func, object } from "../../../../decorators/index.js"

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
