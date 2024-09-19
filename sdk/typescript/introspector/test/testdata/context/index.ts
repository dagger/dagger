import { Directory } from "../../../../api/client.gen.ts"
import { dag } from "../../../../api/client.gen.ts"
import { func, object, argument } from "../../../decorators/decorators.ts"

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
