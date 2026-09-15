import { dag, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  async test(): Promise<string> {
    return await dag.dep().ctl("foo")
  }
}
