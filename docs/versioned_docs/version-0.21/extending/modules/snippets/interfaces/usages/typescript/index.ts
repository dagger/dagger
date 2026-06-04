import { dag, func, object } from "@dagger.io/dagger"

@object()
export class Usage {
  @func()
  async test(): Promise<string> {
    return dag.myModule().foo(dag.example())
  }
}
