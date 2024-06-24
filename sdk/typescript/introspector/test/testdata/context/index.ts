import { func, object, argument } from "../../../decorators/decorators.js"
import { Directory } from "../../../../api/client.gen.js"
import { dag } from "../../../../api/client.gen.js"

@object()
export class Context {
  @func()
  helloWorld(
    @argument({ defaultPath: "." }) 
    dir: Directory,
  ): string {
    return `hello ${name}`
  }

  @func()
  helloWorldIgnored(
    @argument({ defaultPath: ".", ignore: ["dir"] }) 
    dir: Directory,
  ): string {
    return `hello ${name}`
  }
}