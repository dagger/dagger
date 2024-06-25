import { func, object, context } from "../../../decorators/decorators.js"
import { Directory } from "../../../../api/client.gen.js"

@object()
export class Context {
  @func()
  helloWorld(
    @context(".") 
    dir: Directory
  ): string {
    return `hello ${name}`
  }
}
